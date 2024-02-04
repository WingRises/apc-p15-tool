package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"runtime"

	"golang.org/x/crypto/ssh"
)

// cmdInstall is the app's command to create apc p15 file content from key and cert
// pem files and upload the p15 to the specified APC UPS
func (app *app) cmdInstall(cmdCtx context.Context, args []string) error {
	// extra args == error
	if len(args) != 0 {
		return fmt.Errorf("install: failed, %w (%d)", ErrExtraArgs, len(args))
	}

	// must have username
	if app.config.install.username == nil || *app.config.install.username == "" {
		return errors.New("install: failed, username not specified")
	}

	// must have password
	if app.config.install.password == nil || *app.config.install.password == "" {
		return errors.New("install: failed, password not specified")
	}

	// must have fingerprint
	if app.config.install.fingerprint == nil || *app.config.install.fingerprint == "" {
		return errors.New("install: failed, fingerprint not specified")
	}

	keyPem, certPem, err := app.config.install.keyCertPemCfg.GetPemBytes("install")
	if err != nil {
		return err
	}

	// host to install on must be specified
	if app.config.install.hostAndPort == nil || *app.config.install.hostAndPort == "" {
		return errors.New("install: failed, apc host not specified")
	}

	// validation done

	// make p15 file
	apcFile, err := app.pemToAPCP15(keyPem, certPem, "install")
	if err != nil {
		return err
	}

	// make host key callback
	hk := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		// calculate server's key's SHA256
		hasher := sha256.New()
		_, err := hasher.Write(key.Marshal())
		if err != nil {
			return err
		}
		actualHash := hasher.Sum(nil)

		// log fingerprint for debugging
		actualHashB64 := base64.RawStdEncoding.EncodeToString(actualHash)
		actualHashHex := hex.EncodeToString(actualHash)
		app.debugLogger.Printf("ssh: remote server key fingerprint (b64): %s", actualHashB64)
		app.debugLogger.Printf("ssh: remote server key fingerprint (hex): %s", actualHashHex)

		// allow base64 format
		if actualHashB64 == *app.config.install.fingerprint {
			return nil
		}

		// allow hex format
		if actualHashHex == *app.config.install.fingerprint {
			return nil
		}

		return errors.New("ssh: fingerprint didn't match")
	}

	// kex algos
	// see defaults: https://cs.opensource.google/go/x/crypto/+/refs/tags/v0.18.0:ssh/common.go;l=62
	kexAlgos := []string{
		"curve25519-sha256", "curve25519-sha256@libssh.org",
		"ecdh-sha2-nistp256", "ecdh-sha2-nistp384", "ecdh-sha2-nistp521",
		"diffie-hellman-group14-sha256", "diffie-hellman-group14-sha1",
	}
	// extra for some apc ups
	kexAlgos = append(kexAlgos, "diffie-hellman-group-exchange-sha256")

	// install file on UPS
	// ssh config
	config := &ssh.ClientConfig{
		User: *app.config.install.username,
		Auth: []ssh.AuthMethod{
			ssh.Password(*app.config.install.password),
		},
		// APC seems to require `Client Version` string to start with "SSH-2" and must be at least
		// 13 characters long
		// working examples from other clients:
		// ClientVersion: "SSH-2.0-OpenSSH_8.9p1 Ubuntu-3ubuntu0.6",
		// ClientVersion: "SSH-2.0-PuTTY_Release_0.80",
		ClientVersion: fmt.Sprintf("SSH-2.0-apc-p15-tool_v%s %s-%s", appVersion, runtime.GOOS, runtime.GOARCH),
		Config: ssh.Config{
			KeyExchanges: kexAlgos,
			// Ciphers:      []string{"aes128-ctr"},
			// MACs:         []string{"hmac-sha2-256"},
		},
		// HostKeyAlgorithms: []string{"ssh-rsa"},
		HostKeyCallback: hk,

		// reasonable timeout for file copy
		Timeout: scpTimeout,
	}

	// connect to ups over SSH
	client, err := ssh.Dial("tcp", *app.config.install.hostAndPort, config)
	if err != nil {
		return fmt.Errorf("install: failed to connect to host (%w)", err)
	}

	// send file to UPS
	err = scpSendFileToUPS(client, apcFile)
	if err != nil {
		return fmt.Errorf("install: failed to send p15 file to ups over scp (%w)", err)
	}

	// done
	app.stdLogger.Printf("install: apc p15 file installed on %s", *app.config.install.hostAndPort)

	return nil
}

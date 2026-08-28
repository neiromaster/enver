package app

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"os"

	"github.com/neiromaster/enver/internal/crypto"
)

// ErrAborted reports a declined destructive confirm. The CLI layer maps it to
// the shared aborted notice and a zero exit.
var ErrAborted = errors.New("aborted")

// CryptScan is what keygen needs to know about the configs' encrypted values:
// the first enc:v3 salt, its KDF parameters, and the full sample value it
// came from. Salt is nil when the configs hold no encrypted value; a
// mixed-era config errors at the source instead.
type CryptScan struct {
	Salt   []byte
	Params crypto.Argon2Params
	Sample string
}

// KeygenOptions carries the keygen knobs the CLI layer binds flags and TTY
// seams to. Scan is consulted only on overwrite paths that can strand values;
// Confirm asks before a stranding overwrite and maps to the ui layer.
type KeygenOptions struct {
	Path    string
	Random  bool
	Force   bool
	Scan    func() (CryptScan, error)
	Confirm func(msg string, def bool) (bool, error)
}

// Keygen writes the key cache to opts.Path: a random key under Random, else
// one derived from an interactive passphrase and verified against the
// configs' encrypted values. Prompts go through PromptPassphrase; declining
// the stranding confirm returns ErrAborted.
func Keygen(opts KeygenOptions) error {
	// scanConfigs is opts.Scan wrapped with the context the CLI scan helper
	// used to add at the seam.
	scanConfigs := func() (CryptScan, error) {
		scan, err := opts.Scan()
		if err != nil {
			return CryptScan{}, fmt.Errorf("cannot scan configs for encrypted values: %w", err)
		}
		return scan, nil
	}
	if opts.Random {
		risk, err := keygenRisk(opts.Force, opts.Path, nil, func() (bool, error) {
			scan, err := scanConfigs()
			if err != nil {
				return false, err
			}
			return scan.Salt != nil, nil
		})
		if err != nil {
			return err
		}
		if risk {
			fmt.Fprintln(os.Stderr, "warning: overwriting the key makes existing encrypted values unreadable; run `enver decrypt` with the old key first to migrate")
		}
		return crypto.GenerateKey(opts.Path, opts.Force)
	}
	if !Interactive() {
		return fmt.Errorf("keygen requires a terminal; use --random for a non-interactive key")
	}
	if !opts.Force {
		if _, err := os.Stat(opts.Path); err == nil {
			return fmt.Errorf("key already exists at %s (use --force to overwrite)", opts.Path)
		}
	}
	scan, err := scanConfigs()
	if err != nil {
		return err
	}
	pass, err := PromptPassphrase("Enter passphrase:")
	if err != nil {
		return err
	}
	confirm, err := PromptPassphrase("Confirm passphrase:")
	if err != nil {
		return err
	}
	if pass != confirm {
		return fmt.Errorf("passphrases do not match")
	}
	salt := scan.Salt
	params := scan.Params
	if salt == nil {
		salt = make([]byte, crypto.SaltSize)
		if _, err := rand.Read(salt); err != nil {
			return err
		}
		params = crypto.CurrentParams
	}
	key, err := crypto.DeriveKey(pass, salt, params)
	if err != nil {
		return err
	}
	// The key reuses the configs' salt, so a passphrase that cannot decrypt
	// the sample is not the one the existing values were encrypted with.
	// Verified before the risk gate: a passphrase that decrypts the sample
	// strands nothing, so it is never warned about as stranding — it is the
	// recovery path for a lost or rotated key cache. Every non-force path
	// must refuse rather than cache a key no value in the configs opens with.
	matches := false
	if scan.Salt != nil {
		_, derr := crypto.DecryptValue(scan.Sample, key)
		matches = derr == nil
	}
	risk, err := keygenRisk(opts.Force, opts.Path, key, func() (bool, error) { return scan.Salt != nil && !matches, nil })
	if err != nil {
		return err
	}
	if scan.Salt != nil && !matches && !risk {
		return fmt.Errorf("passphrase does not match the encrypted values in the configs; they stay tied to the passphrase that encrypted them — use it, or remove those values first")
	}
	if risk {
		ok, cerr := opts.Confirm(
			"This passphrase derives a key different from the current one. Overwriting strands existing encrypted values (recover them first with the key that encrypted them). Overwrite anyway?",
			false)
		if cerr != nil {
			return cerr
		}
		if !ok {
			return ErrAborted
		}
	}
	return crypto.WriteKeyCache(opts.Path, crypto.NewKeyCache(salt, key))
}

// keygenRisk reports whether force-overwriting the key at path with newKey
// would strand encrypted values: an existing key that differs plus values the
// replacement cannot read. newKey is nil when the key is not yet known
// (--random), in which case any existing key counts as different and any
// encrypted value counts as stranded. wouldStrand is called only when the
// overwrite is real, so paths that can strand nothing (--random or
// interactive keygen without --force) never read the configs: --random is the
// bootstrap path and must work beside unreadable or foreign-enc configs. A
// corrupt key file is an error: overwriting it cannot be judged safe.
func keygenRisk(force bool, path string, newKey []byte, wouldStrand func() (bool, error)) (bool, error) {
	if !force {
		return false, nil
	}
	old, _, err := crypto.LoadKey(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil // no existing key file: nothing to strand
		}
		return false, fmt.Errorf("existing key at %s is unreadable: %w; fix or remove it before forcing a new key", path, err)
	}
	if len(old) == 0 {
		return false, nil
	}
	if newKey != nil && bytes.Equal(old, newKey) {
		return false, nil // same key: safe to rewrite
	}
	return wouldStrand()
}

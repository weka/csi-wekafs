package archive

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

// Argon2id cost parameters. 64 MiB and 3 passes is the parameter set RFC 9106 recommends
// for the memory-constrained case, which suits a CLI that must also run inside a container.
const (
	argonTime    uint32 = 3
	argonMemoryK uint32 = 64 * 1024
	argonThreads uint8  = 4
	argonKeyLen  uint32 = 32
	saltLen             = 16
)

// newKDFParams generates fresh key derivation parameters with a random salt.
func newKDFParams() (*KDFParams, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generating salt: %w", err)
	}
	return &KDFParams{
		Time:    argonTime,
		MemoryK: argonMemoryK,
		Threads: argonThreads,
		SaltB64: base64.StdEncoding.EncodeToString(salt),
	}, nil
}

// deriveKey turns a password into a 32-byte AES key using the container's own parameters,
// so archives written by older versions stay readable after the costs are raised.
func deriveKey(password string, p *KDFParams) ([]byte, error) {
	if p == nil {
		return nil, errors.New("no key derivation parameters")
	}
	salt, err := base64.StdEncoding.DecodeString(p.SaltB64)
	if err != nil {
		return nil, fmt.Errorf("decoding salt: %w", err)
	}
	if len(salt) < saltLen {
		return nil, fmt.Errorf("salt is %d bytes, expected at least %d", len(salt), saltLen)
	}
	if p.Time == 0 || p.MemoryK == 0 || p.Threads == 0 {
		return nil, errors.New("key derivation parameters are out of range")
	}
	return argon2.IDKey([]byte(password), salt, p.Time, p.MemoryK, p.Threads, argonKeyLen), nil
}

// frameAAD binds each frame to the header, to its position in the stream, and to whether it
// terminates the stream. Reordering, splicing between archives, and truncation therefore all
// fail authentication rather than producing a plausible shorter archive.
func frameAAD(headerSum []byte, index uint64, final bool) []byte {
	aad := make([]byte, 0, len(headerSum)+9)
	aad = append(aad, headerSum...)
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], index)
	aad = append(aad, counter[:]...)
	if final {
		return append(aad, 1)
	}
	return append(aad, 0)
}

func frameNonce(index uint64) []byte {
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint64(nonce[4:], index)
	return nonce
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialising cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialising GCM: %w", err)
	}
	return gcm, nil
}

// encryptPayload seals plaintext into length-prefixed AES-256-GCM frames.
func encryptPayload(w io.Writer, plaintext, key, headerSum []byte) error {
	gcm, err := newGCM(key)
	if err != nil {
		return err
	}
	// An empty payload still emits one (empty) final frame, so that decrypt can always
	// assert it saw a terminator.
	for index, offset := uint64(0), 0; ; index++ {
		end := offset + frameSize
		if end >= len(plaintext) {
			end = len(plaintext)
		}
		final := end == len(plaintext)
		sealed := gcm.Seal(nil, frameNonce(index), plaintext[offset:end], frameAAD(headerSum, index, final))
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(sealed)))
		if _, err := w.Write(length[:]); err != nil {
			return fmt.Errorf("writing frame length: %w", err)
		}
		if _, err := w.Write(sealed); err != nil {
			return fmt.Errorf("writing frame: %w", err)
		}
		if final {
			return nil
		}
		offset = end
	}
}

// decryptPayload opens a frame stream, rejecting truncated or reordered input. A failure
// here is indistinguishable from a wrong password, which is intentional.
func decryptPayload(r io.Reader, key, headerSum []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	var (
		out      []byte
		index    uint64
		sawFinal bool
	)
	for !sawFinal {
		var length [4]byte
		if _, err := io.ReadFull(r, length[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil, fmt.Errorf("%w: archive is truncated", ErrIntegrity)
			}
			return nil, fmt.Errorf("reading frame length: %w", err)
		}
		size := binary.BigEndian.Uint32(length[:])
		if size > frameSize+uint32(gcm.Overhead())+1 {
			return nil, fmt.Errorf("%w: frame %d declares an implausible length", ErrIntegrity, index)
		}
		sealed := make([]byte, size)
		if _, err := io.ReadFull(r, sealed); err != nil {
			return nil, fmt.Errorf("%w: archive is truncated", ErrIntegrity)
		}
		// Try the frame as non-final first, then as final. Exactly one can authenticate,
		// because the terminator flag is part of the additional authenticated data.
		plain, err := gcm.Open(nil, frameNonce(index), sealed, frameAAD(headerSum, index, false))
		if err != nil {
			plain, err = gcm.Open(nil, frameNonce(index), sealed, frameAAD(headerSum, index, true))
			if err != nil {
				return nil, fmt.Errorf("%w: wrong password, or the archive has been altered", ErrIntegrity)
			}
			sawFinal = true
		}
		out = append(out, plain...)
		index++
	}
	// Trailing bytes after the terminator mean something was appended.
	if n, _ := io.ReadFull(r, make([]byte, 1)); n != 0 {
		return nil, fmt.Errorf("%w: unexpected trailing data after end of archive", ErrIntegrity)
	}
	return out, nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum)
}

func sha256Raw(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}

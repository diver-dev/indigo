package crypto

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"fmt"
	"log"
	"math/big"

	"github.com/mr-tron/base58"
)

// Implements the [PrivateKeyExportable] and [PrivateKey] interfaces for the NIST P-256 / secp256r1 / ES256 cryptographic curve.
// Secret key material is naively stored in memory.
type PrivateKeyP256 struct {
	privP256 *ecdsa.PrivateKey
}

// Implements the [PublicKey] interface for the NIST P-256 / secp256r1 / ES256 cryptographic curve.
type PublicKeyP256 struct {
	pubP256 *ecdsa.PublicKey
}

var _ PrivateKey = (*PrivateKeyP256)(nil)
var _ PrivateKeyExportable = (*PrivateKeyP256)(nil)
var _ PublicKey = (*PublicKeyP256)(nil)

// Creates a secure new cryptographic key from scratch.
func GeneratePrivateKeyP256() (*PrivateKeyP256, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("P-256/secp256r1 key generation failed: %w", err)
	}
	priv := PrivateKeyP256{privP256: key}
	err = priv.ensureBytes()
	if err != nil {
		return nil, err
	}
	return &priv, nil
}

// Loads a [PrivateKeyP256] from raw bytes, as exported by the PrivateKeyP256.Bytes method.
//
// Calling code needs to know the key type ahead of time, and must remove any string encoding (hex encoding, base64, etc) before calling this function.
func ParsePrivateBytesP256(data []byte) (*PrivateKeyP256, error) {
	// elaborately parse as an ecdh.PrivateKey, then get from that to ecdsa.PrivateKey by encoding/decoding using x509 PKCS8 encoding.
	// Note that the 'data' bytes format is *not* x509 PKCS8!
	skEcdh, err := ecdh.P256().NewPrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("invalid P-256/secp256r1 private key: %w", err)
	}
	enc, err := x509.MarshalPKCS8PrivateKey(skEcdh)
	if err != nil {
		return nil, fmt.Errorf("invalid P-256/secp256r1 private key: %w", err)
	}
	sk, err := x509.ParsePKCS8PrivateKey(enc)
	if err != nil {
		return nil, fmt.Errorf("invalid P-256/secp256r1 private key: %w", err)
	}
	skECDSA, ok := sk.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("expected ECDSA privatekey from internal encoding")
	}
	priv := PrivateKeyP256{privP256: skECDSA}
	err = priv.ensureBytes()
	if err != nil {
		return nil, err
	}
	return &priv, nil
}

// Checks if the two private keys are the same. Note that the naive == operator does not work for most equality checks.
func (k *PrivateKeyP256) Equal(other PrivateKey) bool {
	otherP256, ok := other.(*PrivateKeyP256)
	if ok {
		return k.privP256.Equal(otherP256.privP256)
	}
	return false
}

// internal helper which checks that they key will be possible to export later
func (k *PrivateKeyP256) ensureBytes() error {
	_, err := k.privP256.ECDH()
	return err
}

// Serializes the secret key material in to a raw binary format, which can be parsed by [ParsePrivateBytesP256].
//
// For P-256, this is the "compact" encoding and is 32 bytes long. There is no ASN.1 or other enclosing structure.
func (k *PrivateKeyP256) Bytes() []byte {
	skEcdh, err := k.privP256.ECDH()
	if err != nil {
		log.Fatal("unexpected failure to export P-256 private key, after being exportable at parse time")
	}
	return skEcdh.Bytes()
}

// Outputs the [PublicKey] corresponding to this [PrivateKeyP256]; it will be a [PublicKeyP256].
func (k *PrivateKeyP256) Public() (PublicKey, error) {
	pub := PublicKeyP256{pubP256: k.privP256.Public().(*ecdsa.PublicKey)}
	err := pub.ensureBytes()
	if err != nil {
		return nil, err
	}
	return &pub, nil
}

// First hashes the raw bytes, then signs the digest, returning a binary signature.
//
// SHA-256 is the hash algorithm used, as specified by atproto. Signing digests is the norm for ECDSA, and required by some backend implementations. This method does not "double hash", it simply has name which clarifies that hashing is happening.
//
// Calling code is responsible for any string encoding of signatures (eg, hex or base64). For P-256, the signature is 64 bytes long.
//
// NIST ECDSA signatures can have a "malleability" issue, meaning that there are multiple valid signatures for the same content with the same signing key. This method always returns a "low-S" signature, as required by atproto.
func (k *PrivateKeyP256) HashAndSign(content []byte) ([]byte, error) {
	hash := sha256.Sum256(content)
	r, s, err := ecdsa.Sign(rand.Reader, k.privP256, hash[:])
	if err != nil {
		return nil, fmt.Errorf("crypto error signing with P-256/secp256r1 private key: %w", err)
	}
	s = sigSToLowS_P256(s)
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return sig, nil
}

// Loads a [PublicKeyP256] raw bytes, as exported by the PublicKey.Bytes method. This is the "compressed" curve format.
//
// Calling code needs to know the key type ahead of time, and must remove any string encoding (hex encoding, base64, etc) before calling this function.
func ParsePublicBytesP256(data []byte) (*PublicKeyP256, error) {
	curve := elliptic.P256()
	x, y := elliptic.UnmarshalCompressed(curve, data)
	if x == nil {
		return nil, fmt.Errorf("invalid P-256 public key (x==nil)")
	}
	if !curve.Params().IsOnCurve(x, y) {
		return nil, fmt.Errorf("invalid P-256 public key (not on curve)")
	}
	pubK := &ecdsa.PublicKey{
		Curve: curve,
		X:     x,
		Y:     y,
	}
	pub := PublicKeyP256{pubP256: pubK}
	err := pub.ensureBytes()
	if err != nil {
		return nil, err
	}
	return &pub, nil
}

// Loads a [PublicKeyP256] from raw bytes, as exported by the PublicKey.UncompressedBytes method.
//
// Calling code needs to know the key type ahead of time, and must remove any string encoding (hex encoding, base64, etc) before calling this function.
func ParsePublicUncompressedBytesP256(data []byte) (*PublicKeyP256, error) {
	curve := elliptic.P256()
	x, y := elliptic.Unmarshal(curve, data)
	if x == nil {
		return nil, fmt.Errorf("invalid P-256 public key (x==nil)")
	}
	if !curve.Params().IsOnCurve(x, y) {
		return nil, fmt.Errorf("invalid P-256 public key (not on curve)")
	}
	pubK := &ecdsa.PublicKey{
		Curve: curve,
		X:     x,
		Y:     y,
	}
	pub := PublicKeyP256{pubP256: pubK}
	err := pub.ensureBytes()
	if err != nil {
		return nil, err
	}
	return &pub, nil
}

// Checks if the two public keys are the same. Note that the naive == operator does not work for most equality checks.
func (k *PublicKeyP256) Equal(other PublicKey) bool {
	otherP256, ok := other.(*PublicKeyP256)
	if ok {
		return k.pubP256.Equal(otherP256.pubP256)
	}
	return false
}

// checks that key will be exportable later, both compressed and uncompressed
func (k *PublicKeyP256) ensureBytes() error {
	if !k.pubP256.Curve.IsOnCurve(k.pubP256.X, k.pubP256.Y) {
		return fmt.Errorf("unexpected invalid P-256/secp256r1 public key (internal)")
	}
	_, err := k.pubP256.ECDH()
	return err
}

// Serializes the key in to "uncompressed" binary format.
func (k *PublicKeyP256) UncompressedBytes() []byte {
	pkEcdh, err := k.pubP256.ECDH()
	if err != nil {
		log.Fatal("unexpected invalid P-256/secp256r1 public key, was verified at parse time")
	}
	return pkEcdh.Bytes()
}

// Serializes the key in to "compressed" binary format.
func (k *PublicKeyP256) Bytes() []byte {
	return elliptic.MarshalCompressed(k.pubP256.Curve, k.pubP256.X, k.pubP256.Y)
}

// Hashes the raw bytes using SHA-256, then verifies the signature against the digest bytes.
//
// Signing digests is the norm for ECDSA, and required by some backend implementations. This method does not "double hash", it simply has name which clarifies that hashing is happening.
//
// Calling code is responsible for any string decoding of signatures (eg, hex or base64) before calling this function.
//
// This method requires a "low-S" signature, as specified by atproto.
func (k *PublicKeyP256) HashAndVerify(content, sig []byte) error {
	hash := sha256.Sum256(content)
	// parseP256Sig
	if len(sig) != 64 {
		return fmt.Errorf("crypto: P-256 signatures must be 64 bytes, got len=%d", len(sig))
	}
	r := big.NewInt(0)
	s := big.NewInt(0)
	r.SetBytes(sig[:32])
	s.SetBytes(sig[32:])

	if !ecdsa.Verify(k.pubP256, hash[:], r, s) {
		return ErrInvalidSignature
	}

	// ensure that signature is low-S
	if !sigSIsLowS_P256(s) {
		return ErrInvalidSignature
	}

	return nil
}

// Multibase string encoding of the public key, including a multicodec indicator and compressed curve bytes serialization
func (k *PublicKeyP256) Multibase() string {
	kbytes := k.Bytes()
	// multicodec p256-pub, code 0x1200, varint-encoded bytes: [0x80, 0x24]
	kbytes = append([]byte{0x80, 0x24}, kbytes...)
	return "z" + base58.Encode(kbytes)
}

// did:key string encoding of the public key, as would be encoded in a DID PLC operation:
//
//   - compressed / compacted binary representation
//   - prefix with appropriate curve multicodec bytes
//   - encode bytes with base58btc
//   - add "z" prefix to indicate encoding
//   - add "did:key:" prefix
func (k *PublicKeyP256) DIDKey() string {
	return "did:key:" + k.Multibase()
}
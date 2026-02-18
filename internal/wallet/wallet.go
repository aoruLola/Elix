package wallet

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"

	bip39 "github.com/cosmos/go-bip39"
)

type Identity struct {
	Mnemonic   string `json:"mnemonic,omitempty"`
	Address    string `json:"address"`
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key,omitempty"`
}

func GenerateIdentity(entropyBits int, passphrase string) (Identity, error) {
	if entropyBits == 0 {
		entropyBits = 256
	}
	entropy, err := bip39.NewEntropy(entropyBits)
	if err != nil {
		return Identity{}, err
	}
	mnemonic, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return Identity{}, err
	}
	id, err := RecoverIdentity(mnemonic, passphrase)
	if err != nil {
		return Identity{}, err
	}
	id.Mnemonic = mnemonic
	return id, nil
}

func RecoverIdentity(mnemonic, passphrase string) (Identity, error) {
	if !bip39.IsMnemonicValid(mnemonic) {
		return Identity{}, errors.New("invalid mnemonic")
	}
	seed := bip39.NewSeed(mnemonic, passphrase)
	if len(seed) < ed25519.SeedSize {
		return Identity{}, errors.New("invalid seed size")
	}
	privateKey := ed25519.NewKeyFromSeed(seed[:ed25519.SeedSize])
	publicKey := privateKey.Public().(ed25519.PublicKey)
	return Identity{
		Address:    AddressFromPublicKey(publicKey),
		PublicKey:  base64.RawURLEncoding.EncodeToString(publicKey),
		PrivateKey: base64.RawURLEncoding.EncodeToString(privateKey),
	}, nil
}

func AddressFromPublicKey(publicKey []byte) string {
	sum := sha256.Sum256(publicKey)
	return "elix1" + hex.EncodeToString(sum[:20])
}

func SignChallenge(privateKeyBase64, challenge string) (string, error) {
	privateKey, err := decodePrivateKey(privateKeyBase64)
	if err != nil {
		return "", err
	}
	sig := ed25519.Sign(privateKey, []byte(challenge))
	return base64.RawURLEncoding.EncodeToString(sig), nil
}

func VerifyChallenge(publicKeyBase64, challenge, signatureBase64 string) (bool, error) {
	pub, err := decodeBase64Flexible(publicKeyBase64)
	if err != nil {
		return false, err
	}
	if len(pub) != ed25519.PublicKeySize {
		return false, fmt.Errorf("public key size must be %d", ed25519.PublicKeySize)
	}
	sig, err := decodeBase64Flexible(signatureBase64)
	if err != nil {
		return false, err
	}
	if len(sig) != ed25519.SignatureSize {
		return false, fmt.Errorf("signature size must be %d", ed25519.SignatureSize)
	}
	return ed25519.Verify(ed25519.PublicKey(pub), []byte(challenge), sig), nil
}

func decodePrivateKey(v string) (ed25519.PrivateKey, error) {
	raw, err := decodeBase64Flexible(v)
	if err != nil {
		return nil, err
	}
	switch len(raw) {
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(raw), nil
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	default:
		return nil, fmt.Errorf("private key size must be %d or %d", ed25519.PrivateKeySize, ed25519.SeedSize)
	}
}

func decodeBase64Flexible(v string) ([]byte, error) {
	decoders := []*base64.Encoding{
		base64.RawURLEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.StdEncoding,
	}
	for _, enc := range decoders {
		if b, err := enc.DecodeString(v); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("invalid base64 payload")
}

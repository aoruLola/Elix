package wallet

import "testing"

func TestRecoverIdentityDeterministic(t *testing.T) {
	mnemonic := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	id1, err := RecoverIdentity(mnemonic, "")
	if err != nil {
		t.Fatalf("recover #1: %v", err)
	}
	id2, err := RecoverIdentity(mnemonic, "")
	if err != nil {
		t.Fatalf("recover #2: %v", err)
	}
	if id1.Address != id2.Address || id1.PublicKey != id2.PublicKey || id1.PrivateKey != id2.PrivateKey {
		t.Fatalf("recovery is not deterministic")
	}
	if id1.Address == "" || id1.PublicKey == "" || id1.PrivateKey == "" {
		t.Fatalf("unexpected empty identity fields: %#v", id1)
	}
}

func TestSignAndVerifyChallenge(t *testing.T) {
	id, err := GenerateIdentity(256, "")
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	challenge := "challenge-123"
	sig, err := SignChallenge(id.PrivateKey, challenge)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	ok, err := VerifyChallenge(id.PublicKey, challenge, sig)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Fatalf("expected signature verification success")
	}
}

package main

import "testing"

var testParams = argonParams{memory: 8 * 1024, time: 1, threads: 1, saltLen: 16, keyLen: 32}

func TestVerifyCorrectPassword(t *testing.T) {
	h, err := hashPassword("correct-password", testParams)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := verifyPassword("correct-password", h)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("correct password was rejected")
	}
}

func TestRejectWrongPassword(t *testing.T) {
	h, _ := hashPassword("correct-password", testParams)
	ok, err := verifyPassword("wrong-password", h)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("wrong password was accepted")
	}
}

func TestSaltMakesHashesDiffer(t *testing.T) {
	a, _ := hashPassword("same-password", testParams)
	b, _ := hashPassword("same-password", testParams)
	if a == b {
		t.Error("two hashes of the same password are identical: salt is not applied")
	}
}

func TestOldParamsStillVerify(t *testing.T) {
	old := argonParams{memory: 8 * 1024, time: 1, threads: 1, saltLen: 16, keyLen: 32}
	h, err := hashPassword("password", old)
	if err != nil {
		t.Fatal(err)
	}

	ok, err := verifyPassword("password", h)
	if err != nil {
		t.Fatalf("failed to parse hash created with old params: %v", err)
	}
	if !ok {
		t.Error("hash created with old params failed to verify")
	}
}

func TestRejectMalformedHash(t *testing.T) {
	for _, bad := range []string{
		"",
		"not a hash at all",
		"$argon2i$v=19$m=8192,t=1,p=1$c29sdA$aGFzaA",
		"$argon2id$v=99$m=8192,t=1,p=1$c29sdA$aGFzaA",
		"$argon2id$v=19$m=8192,t=1,p=1$!!!$aGFzaA",
	} {
		if _, err := verifyPassword("password", bad); err == nil {
			t.Errorf("malformed hash %q was accepted without error", bad)
		}
	}
}

package password

import "testing"

func TestPasswordPolicy(t *testing.T) {
	manager := New()
	valid := []string{"StrongPass1", "Abcdefg1"}
	for _, value := range valid {
		if err := manager.Validate(value); err != nil {
			t.Fatalf("expected %q valid: %v", value, err)
		}
	}
	invalid := []string{"Short1A", "alllowercase1", "ALLUPPERCASE1", "NoDigitsHere", "Abcdefgh1" + string(make([]byte, 64))}
	for _, value := range invalid {
		if err := manager.Validate(value); err == nil {
			t.Fatalf("expected %q invalid", value)
		}
	}
}
func TestHashAndCompare(t *testing.T) {
	manager := New()
	hash, err := manager.Hash("StrongPass1")
	if err != nil {
		t.Fatal(err)
	}
	if hash == "StrongPass1" {
		t.Fatal("password was not hashed")
	}
	if err = manager.Compare(hash, "StrongPass1"); err != nil {
		t.Fatal(err)
	}
	if err = manager.Compare(hash, "WrongPass1"); err == nil {
		t.Fatal("wrong password matched")
	}
}

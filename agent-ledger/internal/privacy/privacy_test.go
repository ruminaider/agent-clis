package privacy

import "testing"

func TestIsLikelySecret(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"prose", "Mapped Tonic statuses to credential errors.", false},
		{"aws_key", "user said: AKIAABCDEFGHIJKLMNOP", true},
		{"bearer", "Authorization: Bearer abc123def456ghijkl", true},
		{"pem", "-----BEGIN OPENSSH PRIVATE KEY-----\nabcd\n-----END OPENSSH PRIVATE KEY-----", true},
		{"ssh_rsa", "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQDxxxx user@host", true},
		{"openai", "key=sk-abcdefghijklmnopqrstuvwxyz0123", true},
		{"github_pat", "token=ghp_abcdefghijklmnopqrstuvwxyz012345", true},
		{"env_dump", "PATH=/usr/bin\nHOME=/root\nSHELL=/bin/bash", true},
		{"single_env_line", "PATH=/usr/bin", false},
		{"client_secret_pair", "client_secret=verysecretvalue123", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsLikelySecret(tc.in); got != tc.want {
				t.Fatalf("IsLikelySecret(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestAssertSafe(t *testing.T) {
	if err := AssertSafe("summary", "ordinary text"); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if err := AssertSafe("summary", "AKIAABCDEFGHIJKLMNOP leaked"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestIsForbiddenKey(t *testing.T) {
	if !IsForbiddenKey("env") {
		t.Fatal("env should be forbidden")
	}
	if IsForbiddenKey("path") {
		t.Fatal("path should be allowed")
	}
}

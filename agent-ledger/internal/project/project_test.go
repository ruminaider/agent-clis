package project

import (
	"strings"
	"testing"
)

func TestCompute_StableAcrossMoves(t *testing.T) {
	in1 := Inputs{
		ProjectID:    "github.com/example/repo",
		OriginURL:    "git@github.com:example/repo.git",
		GitCommonDir: "/orig/path/.git",
	}
	in2 := in1
	id1 := Compute(in1)
	id2 := Compute(in2)
	if id1.Fingerprint != id2.Fingerprint {
		t.Fatalf("fingerprints differ: %q vs %q", id1.Fingerprint, id2.Fingerprint)
	}
	if len(id1.Fingerprint) != FingerprintLen {
		t.Fatalf("fingerprint len = %d", len(id1.Fingerprint))
	}
	for _, c := range id1.Fingerprint {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("non-hex rune %q", c)
		}
	}
}

func TestCompute_DistinctProjects(t *testing.T) {
	a := Compute(Inputs{ProjectID: "a"}).Fingerprint
	b := Compute(Inputs{ProjectID: "b"}).Fingerprint
	if a == b {
		t.Fatal("distinct project_ids should produce distinct fingerprints")
	}
}

func TestCompute_GitCommonDirSuppressesRoot(t *testing.T) {
	withRoot := Compute(Inputs{GitCommonDir: "/x/.git", NonGitRoot: "/wt-a"})
	moved := Compute(Inputs{GitCommonDir: "/x/.git", NonGitRoot: "/wt-b"})
	if withRoot.Fingerprint != moved.Fingerprint {
		t.Fatal("worktrees of same repo should share fingerprint")
	}
}

func TestCompute_NonGitUsesRoot(t *testing.T) {
	a := Compute(Inputs{NonGitRoot: "/a"}).Fingerprint
	b := Compute(Inputs{NonGitRoot: "/b"}).Fingerprint
	if a == b {
		t.Fatal("distinct non-git roots should produce distinct fingerprints")
	}
}

func TestSlug_Sanitizes(t *testing.T) {
	cases := map[string]string{
		"":                            "project",
		"github.com/Foo/Bar":          "github-com-foo-bar",
		"  Recora Health/Shima Enaga": "recora-health-shima-enaga",
		"!!!":                         "project",
		"weird___---name":             "weird___-name",
		strings.Repeat("a", 200):      strings.Repeat("a", SlugMaxLen),
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlugSource_Origin(t *testing.T) {
	cases := map[string]string{
		"https://github.com/foo/bar.git": "foo/bar",
		"git@github.com:foo/bar.git":     "foo/bar",
		"ssh://git@host/foo/bar":         "foo/bar",
	}
	for in, want := range cases {
		if got := originSlugSource(in); got != want {
			t.Errorf("originSlugSource(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDirName(t *testing.T) {
	id := Compute(Inputs{ProjectID: "example.com/test/repo"})
	dn := id.DirName()
	if !strings.HasSuffix(dn, "-"+id.Fingerprint) {
		t.Fatalf("DirName=%q", dn)
	}
	if !strings.HasPrefix(dn, id.Slug+"-") {
		t.Fatalf("DirName=%q", dn)
	}
}

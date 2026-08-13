package orgrename

import "testing"

const newOrg = "cloud-in-a-bottle"

func TestShipsInertUntilTheRenameIsDone(t *testing.T) {
	// NewOrg is settled data; acting on it is a separate switch. Rewriting
	// before the org is renamed would point instances at an owner that does
	// not resolve, so the switch must ship false.
	if OrgRenameComplete {
		t.Fatal("OrgRenameComplete must ship false; flip it only with the rename")
	}
	if Enabled() {
		t.Fatal("Enabled() must be false while OrgRenameComplete is false")
	}
	// The owner must not move while the switch is off. (The repo segment may:
	// that rename already happened -- see TestRepoMoveIsActive.)
	feed := "https://raw.githubusercontent.com/" + OldOrg + "/app-manifest/main/catalog.json"
	if got, changed := Rewrite(feed); changed || got != feed {
		t.Fatalf("disabled rewrite changed the URL: %q changed=%v", got, changed)
	}
}

func TestRepoMoveIsActiveWhileTheOrgMoveIsGated(t *testing.T) {
	// openhost-apps -> app-manifest has already happened, so instances should
	// move off the old path now. The owner stays put until the org rename.
	in := "https://raw.githubusercontent.com/" + OldOrg + "/openhost-apps/main/catalog.json"
	want := "https://raw.githubusercontent.com/" + OldOrg + "/app-manifest/main/catalog.json"
	got, changed := Rewrite(in)
	if !changed || got != want {
		t.Fatalf("got %q changed=%v, want %q true", got, changed, want)
	}
}

func TestRepoMoveLeavesUnknownReposAlone(t *testing.T) {
	in := "https://github.com/" + OldOrg + "/bottled-navidrome"
	if got, changed := RewriteRepo(in); changed || got != in {
		t.Fatalf("rewrote %q -> %q; expected untouched", in, got)
	}
}

func TestRepoMoveIsIdempotent(t *testing.T) {
	in := "https://raw.githubusercontent.com/" + OldOrg + "/app-manifest/main/catalog.json"
	if got, changed := RewriteRepo(in); changed || got != in {
		t.Fatalf("second pass changed %q -> %q", in, got)
	}
}

func TestRewritesTheFeedURL(t *testing.T) {
	in := "https://raw.githubusercontent.com/" + OldOrg + "/openhost-apps/main/catalog.json"
	want := "https://raw.githubusercontent.com/" + newOrg + "/openhost-apps/main/catalog.json"
	got, changed := RewriteOwner(in, OldOrg, newOrg)
	if !changed || got != want {
		t.Fatalf("got %q changed=%v, want %q true", got, changed, want)
	}
}

func TestRewritesGithubComURLs(t *testing.T) {
	in := "https://github.com/" + OldOrg + "/openhost-apps"
	want := "https://github.com/" + newOrg + "/openhost-apps"
	if got, changed := RewriteOwner(in, OldOrg, newOrg); !changed || got != want {
		t.Fatalf("got %q changed=%v, want %q true", got, changed, want)
	}
}

func TestOwnerMatchIsCaseInsensitive(t *testing.T) {
	in := "https://github.com/Imbue-OpenHost/openhost-apps"
	want := "https://github.com/" + newOrg + "/openhost-apps"
	if got, changed := RewriteOwner(in, OldOrg, newOrg); !changed || got != want {
		t.Fatalf("got %q changed=%v, want %q true", got, changed, want)
	}
}

func TestPreservesQueryAndDeepPaths(t *testing.T) {
	in := "https://raw.githubusercontent.com/" + OldOrg + "/openhost-apps/main/catalog.json?v=2"
	want := "https://raw.githubusercontent.com/" + newOrg + "/openhost-apps/main/catalog.json?v=2"
	if got, changed := RewriteOwner(in, OldOrg, newOrg); !changed || got != want {
		t.Fatalf("got %q changed=%v, want %q true", got, changed, want)
	}
}

func TestLeavesEverythingElseAlone(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"other owner", "https://raw.githubusercontent.com/imbue-ai/x/main/catalog.json"},
		{"look-alike host", "https://github.com.evil.example/" + OldOrg + "/x"},
		{"not github", "https://notgithub.com/" + OldOrg + "/x"},
		{"other forge", "https://gitlab.com/" + OldOrg + "/x"},
		{"ssh", "git@github.com:" + OldOrg + "/x.git"},
		{"no repo segment", "https://github.com/" + OldOrg},
		{"owner-shaped repo name under another owner", "https://github.com/someone/" + OldOrg},
		{"empty", ""},
		{"file url", "file:///srv/catalog.json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, changed := RewriteOwner(c.in, OldOrg, newOrg)
			if changed || got != c.in {
				t.Fatalf("rewrote %q -> %q (changed=%v); expected untouched", c.in, got, changed)
			}
		})
	}
}

func TestRepoNamedAfterTheOwnerKeepsItsName(t *testing.T) {
	in := "https://github.com/" + OldOrg + "/" + OldOrg + "-tools"
	want := "https://github.com/" + newOrg + "/" + OldOrg + "-tools"
	if got, changed := RewriteOwner(in, OldOrg, newOrg); !changed || got != want {
		t.Fatalf("got %q changed=%v, want %q true", got, changed, want)
	}
}

func TestIdempotent(t *testing.T) {
	in := "https://github.com/" + OldOrg + "/openhost-apps"
	once, _ := RewriteOwner(in, OldOrg, newOrg)
	twice, changed := RewriteOwner(once, OldOrg, newOrg)
	if changed || twice != once {
		t.Fatalf("second pass changed the URL: %q -> %q", once, twice)
	}
}

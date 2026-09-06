package registry

import (
	"encoding/json"
	"strings"
	"testing"
)

const twoAccounts = `{
 "version": 1,
 "generated": "2026-09-06",
 "accounts": [
  {"name": "Queen", "aurora_id": 18242965, "battle_tag": "kmw",
   "toons": [{"toon": "Queennnnnn", "gateway": 30},
             {"toon": "lllIIllIIllIII", "gateway": 11, "games_last_week": 18}],
   "source": "bnet-webapi", "last_verified": "2026-09-06"},
  {"name": "Queen", "aurora_id": 999, "toons": [{"toon": "QueenAlt", "gateway": 20}],
   "source": "cwal", "last_verified": "2026-08-08"}
 ]
}`

func mustParse(t *testing.T, doc string) *Registry {
	t.Helper()
	r, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestLookups(t *testing.T) {
	r := mustParse(t, twoAccounts)

	if r.Len() != 2 || r.ToonCount() != 3 {
		t.Fatalf("Len=%d ToonCount=%d, want 2 and 3", r.Len(), r.ToonCount())
	}

	// Toon lookup is case-insensitive and space-tolerant, because the name
	// arrives from replay slots and command lines, not from this file.
	for _, q := range []string{"Queennnnnn", "queennnnnn", "  QUEENNNNNN  "} {
		a, ok := r.LookupToon(q)
		if !ok || a.AuroraID != 18242965 {
			t.Fatalf("LookupToon(%q) = %+v, %v", q, a, ok)
		}
	}
	if _, ok := r.LookupToon("nobody"); ok {
		t.Fatal("LookupToon found an account for an unknown toon")
	}

	// The same toon name can exist on two gateways, so the gateway-specific
	// lookup must not answer for the wrong one.
	if _, ok := r.LookupToonOnGateway("Queennnnnn", 30); !ok {
		t.Fatal("LookupToonOnGateway missed the right gateway")
	}
	if _, ok := r.LookupToonOnGateway("Queennnnnn", 20); ok {
		t.Fatal("LookupToonOnGateway answered for the wrong gateway")
	}

	if a, ok := r.LookupAurora(999); !ok || a.Source != "cwal" {
		t.Fatalf("LookupAurora(999) = %+v, %v", a, ok)
	}

	// A pro holding several accounts is the normal case, not an edge case.
	if got := r.LookupName("queen"); len(got) != 2 {
		t.Fatalf("LookupName returned %d accounts, want 2", len(got))
	}
	if got := r.LookupName("nobody"); len(got) != 0 {
		t.Fatalf("LookupName for an unknown name returned %d accounts", len(got))
	}
	if names := r.Names(); len(names) != 1 || names[0] != "Queen" {
		t.Fatalf("Names() = %v, want one Queen", names)
	}
}

func TestPrimaryToonPicksTheActiveOne(t *testing.T) {
	r := mustParse(t, twoAccounts)
	a, _ := r.LookupAurora(18242965)
	got, ok := a.PrimaryToon()
	if !ok || got.Name != "lllIIllIIllIII" {
		t.Fatalf("PrimaryToon = %+v, %v; want the toon with games_last_week", got, ok)
	}

	// With no activity data at all it still has to answer, or a refresh has
	// no toon to enter the account through.
	none := Account{Toons: []Toon{{Name: "a", Gateway: 30}, {Name: "b", Gateway: 20}}}
	if got, ok := none.PrimaryToon(); !ok || got.Name != "a" {
		t.Fatalf("PrimaryToon with no activity = %+v, %v", got, ok)
	}
	if _, ok := (Account{}).PrimaryToon(); ok {
		t.Fatal("PrimaryToon claimed a toon on an account with none")
	}
}

func TestDuplicateAuroraIsRejected(t *testing.T) {
	// Two entries for one account is a curation bug and silently keeping one
	// would hide it.
	doc := `{"version":1,"accounts":[{"aurora_id":7,"toons":[]},{"aurora_id":7,"toons":[]}]}`
	if _, err := Parse([]byte(doc)); err == nil || !strings.Contains(err.Error(), "two accounts") {
		t.Fatalf("Parse err = %v, want a duplicate-aurora error", err)
	}
}

func TestUpsertUnionsToonsAndKeepsKnownFields(t *testing.T) {
	r := mustParse(t, twoAccounts)

	// A refresh sees only the toons that exist today. A toon that has gone
	// quiet is still a true fact about the account, so it must survive.
	r.Upsert(Account{
		AuroraID:     18242965,
		Toons:        []Toon{{Name: "Queennnnnn", Gateway: 30, GamesLastWeek: 4}, {Name: "brandnew", Gateway: 45}},
		Source:       "bnet-webapi",
		LastVerified: "2026-09-07",
	})

	a, _ := r.LookupAurora(18242965)
	if a.Name != "Queen" || a.BattleTag != "kmw" {
		t.Fatalf("Upsert dropped known fields: %+v", a)
	}
	if len(a.Toons) != 3 {
		t.Fatalf("Upsert left %d toons, want 3 (union)", len(a.Toons))
	}
	var refreshed bool
	for _, tn := range a.Toons {
		if tn.Name == "Queennnnnn" && tn.GamesLastWeek == 4 {
			refreshed = true
		}
	}
	if !refreshed {
		t.Fatal("Upsert did not update the activity of an existing toon")
	}
	if _, ok := r.LookupToon("brandnew"); !ok {
		t.Fatal("Upsert did not reindex the new toon")
	}

	// A wholly new account appends rather than replacing.
	r.Upsert(Account{Name: "Larva", AuroraID: 1234, Toons: []Toon{{Name: "JSA_Larva", Gateway: 30}}})
	if r.Len() != 3 {
		t.Fatalf("Len = %d after inserting a new account, want 3", r.Len())
	}
	if _, ok := r.LookupToon("JSA_Larva"); !ok {
		t.Fatal("new account's toon is not indexed")
	}
}

func TestMarshalIsCanonicalAndRoundTrips(t *testing.T) {
	r := mustParse(t, twoAccounts)
	// Insert out of order to prove Marshal sorts rather than preserving
	// insertion order: a refresh should produce a reviewable diff.
	r.Upsert(Account{Name: "Aaa", AuroraID: 1, Toons: []Toon{{Name: "z", Gateway: 45}, {Name: "a", Gateway: 30}}})
	r.Upsert(Account{AuroraID: 2, Toons: []Toon{{Name: "orphan", Gateway: 30}}})

	data, err := r.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var doc Registry
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Accounts[0].Name != "Aaa" {
		t.Fatalf("accounts not sorted by name: first is %q", doc.Accounts[0].Name)
	}
	// An unattributed account sorts last, not first, so the readable part of
	// the file stays at the top.
	if doc.Accounts[len(doc.Accounts)-1].Name != "" {
		t.Fatalf("unattributed account is not last: %+v", doc.Accounts[len(doc.Accounts)-1])
	}
	if doc.Accounts[0].Toons[0].Gateway != 30 {
		t.Fatalf("toons not sorted by gateway: %+v", doc.Accounts[0].Toons)
	}

	// Marshalling twice must be byte-identical, or every refresh churns the diff.
	again, err := r.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(again) {
		t.Fatal("Marshal is not deterministic")
	}

	// An empty toon list serializes as [] so consumers need no null check.
	if !strings.Contains(string(data), `"toons": [`) {
		t.Fatal("expected toons to serialize as an array")
	}
	if strings.Contains(string(data), `"toons": null`) {
		t.Fatal("toons serialized as null")
	}
}

func TestEmbeddedRegistryIsUsable(t *testing.T) {
	r, err := LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != 1 {
		t.Fatalf("embedded registry version = %d, want 1", r.Version)
	}
	if r.Len() == 0 {
		t.Fatal("embedded registry is empty")
	}
	// Provenance is the point of shipping this: an entry with no source or
	// no verification date cannot be judged for staleness.
	for _, a := range r.Accounts {
		if a.Source == "" || a.LastVerified == "" {
			t.Fatalf("account %d (%s) has no source/last_verified", a.AuroraID, a.Name)
		}
	}
}

func TestGatewayName(t *testing.T) {
	if got := GatewayName(30); got != "Korea" {
		t.Fatalf("GatewayName(30) = %q", got)
	}
	// An unknown gateway must still carry its number rather than vanish.
	if got := GatewayName(77); !strings.Contains(got, "77") {
		t.Fatalf("GatewayName(77) = %q, want the number to survive", got)
	}
}

func TestUnsetNameRemovesAnAttributionUpsertWouldKeep(t *testing.T) {
	r := mustParse(t, twoAccounts)

	// Upsert deliberately keeps a known name when the incoming record has
	// none, so taking an attribution off needs its own operation — otherwise
	// a name the fingerprints have refuted could never be removed.
	r.Upsert(Account{AuroraID: 999, Source: "refuted", LastVerified: "2026-09-06"})
	if a, _ := r.LookupAurora(999); a.Name != "Queen" {
		t.Fatalf("Upsert dropped the name without being asked: %+v", a)
	}

	r.UnsetName(999)
	a, ok := r.LookupAurora(999)
	if !ok || a.Name != "" {
		t.Fatalf("UnsetName left the name in place: %+v", a)
	}
	// The account itself must survive: deleting it would lose a real
	// Battle.net account and hide the conflict that prompted the removal.
	if r.Len() != 2 {
		t.Fatalf("UnsetName removed the account: Len = %d", r.Len())
	}
	// And it must drop out of the by-name index, or lookups would still
	// report the refuted attribution.
	for _, got := range r.LookupName("Queen") {
		if got.AuroraID == 999 {
			t.Fatal("an unattributed account is still indexed by its old name")
		}
	}
	if _, ok := r.LookupToon("QueenAlt"); !ok {
		t.Fatal("UnsetName broke the toon index")
	}

	// An unknown aurora id is a no-op, not a panic.
	r.UnsetName(12345)
}

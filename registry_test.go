package scfingerprint

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/icza/screp/repparser"
	"github.com/marianogappa/scfingerprint/internal/features"
	"github.com/marianogappa/scfingerprint/internal/registry"
)

// newTestRegistry builds a Registry from a literal document, so tests do not
// have to move every time the shipped map is refreshed.
func newTestRegistry(t *testing.T, doc string) *Registry {
	t.Helper()
	inner, err := registry.Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return &Registry{inner: inner}
}

func TestBuiltinRegistryIsLoadableAndIndexed(t *testing.T) {
	reg, err := BuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if reg.Len() == 0 || reg.ToonCount() == 0 {
		t.Fatalf("registry has %d accounts and %d toons", reg.Len(), reg.ToonCount())
	}
	if reg.Version() != 1 || reg.Generated() == "" {
		t.Fatalf("version = %d, generated = %q", reg.Version(), reg.Generated())
	}

	names := reg.Names()
	if len(names) == 0 {
		t.Fatal("registry names no players")
	}

	// Every name must resolve back to at least one account, and every account
	// that has toons must be reachable by one of them — otherwise the map
	// ships lookups that cannot answer.
	for _, name := range names {
		accs := reg.LookupName(name)
		if len(accs) == 0 {
			t.Fatalf("LookupName(%q) found nothing despite being listed", name)
		}
	}
	for _, a := range reg.Accounts() {
		if a.Source == "" || a.LastVerified == "" {
			t.Fatalf("account %d has no provenance: %+v", a.AuroraID, a)
		}
		if got, ok := reg.LookupAurora(a.AuroraID); !ok || got.AuroraID != a.AuroraID {
			t.Fatalf("LookupAurora(%d) failed", a.AuroraID)
		}
		for _, tn := range a.Toons {
			if _, ok := reg.LookupToon(tn.Toon); !ok {
				t.Fatalf("toon %q of account %d is not indexed", tn.Toon, a.AuroraID)
			}
			if tn.GatewayName == "" {
				t.Fatalf("toon %q has no rendered gateway name", tn.Toon)
			}
			if _, ok := reg.LookupToonOnGateway(tn.Toon, tn.Gateway); !ok {
				t.Fatalf("toon %q is not findable on its own gateway %d", tn.Toon, tn.Gateway)
			}
		}
	}

	if _, ok := reg.LookupToon("definitely-not-a-progamer-toon"); ok {
		t.Fatal("registry claimed an unknown toon")
	}
}

func TestGatewayNameIsExported(t *testing.T) {
	if GatewayName(30) != "Korea" {
		t.Fatalf("GatewayName(30) = %q", GatewayName(30))
	}
}

// twoAccountRegistry maps two toons to two different players.
func twoAccountRegistry(t *testing.T) *Registry {
	t.Helper()
	return newTestRegistry(t, `{
	 "version": 1, "generated": "2026-09-06",
	 "accounts": [
	  {"name": "dummy", "aurora_id": 1, "battle_tag": "bt",
	   "toons": [{"toon": "toon-one", "gateway": 30}],
	   "source": "test", "last_verified": "2026-09-06"},
	  {"name": "somebodyelse", "aurora_id": 2,
	   "toons": [{"toon": "toon-two", "gateway": 30}],
	   "source": "test", "last_verified": "2026-09-06"}
	 ]}`)
}

func TestRegistryOpinionAgreesAndDisagrees(t *testing.T) {
	reg := twoAccountRegistry(t)

	// The registry says toon-one is "dummy". A candidate labelled "dummy"
	// agrees; one labelled anything else disagrees, and disagreement is the
	// interesting signal rather than an error.
	op := registryOpinion([]PlayerGame{{Toon: "toon-one"}}, reg)
	if op == nil {
		t.Fatal("no opinion for a registered toon")
	}
	if op.Name != "dummy" || op.AuroraID != 1 || op.BattleTag != "bt" {
		t.Fatalf("opinion = %+v", op)
	}
	if !op.forLabel("dummy").Agrees || !op.forLabel("DUMMY").Agrees {
		t.Fatal("agreement is not case-insensitive")
	}
	if op.forLabel("Larva").Agrees {
		t.Fatal("opinion agreed with the wrong label")
	}
	// forLabel must not mutate the shared opinion, or the last candidate's
	// verdict would leak onto every other result.
	if op.Agrees {
		t.Fatal("forLabel mutated the source opinion")
	}

	// A toon nobody has registered gets silence, not a guess: most accounts
	// are not progamer accounts.
	if got := registryOpinion([]PlayerGame{{Toon: "stranger"}}, reg); got != nil {
		t.Fatalf("opinion for an unregistered toon = %+v", got)
	}
	// No toon at all, and no registry at all, are both silence too.
	if got := registryOpinion([]PlayerGame{{Vector: []float64{1}}}, reg); got != nil {
		t.Fatalf("opinion with no toon = %+v", got)
	}
	if got := registryOpinion([]PlayerGame{{Toon: "toon-one"}}, nil); got != nil {
		t.Fatalf("opinion with no registry = %+v", got)
	}
}

func TestRegistryOpinionFlagsMixedAccounts(t *testing.T) {
	reg := twoAccountRegistry(t)

	// A probe whose games were played on two different registered accounts
	// is itself worth seeing — it is either mislabelled evidence or a
	// genuinely shared identity.
	op := registryOpinion([]PlayerGame{{Toon: "toon-one"}, {Toon: "toon-two"}}, reg)
	if op == nil || !op.Ambiguous {
		t.Fatalf("opinion = %+v, want Ambiguous", op)
	}

	// The same account twice is not ambiguous.
	op = registryOpinion([]PlayerGame{{Toon: "toon-one"}, {Toon: "TOON-ONE"}}, reg)
	if op == nil || op.Ambiguous {
		t.Fatalf("opinion = %+v, want unambiguous", op)
	}
}

func TestWithRegistryNeverChangesTheScore(t *testing.T) {
	// The whole design rests on this: a registry opinion is reported next to
	// the fingerprint verdict and never folded into it. If passing the option
	// moved a single number, the registry would be acting as evidence.
	scorer := testScorer(t)
	db, err := newDatasetWithScorer(scorer)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("internal", "features", "testdata", "01_zvt_zergling_rush.rep")
	r, err := repparser.ParseFileConfig(path, repparser.Config{Commands: true})
	if err != nil {
		t.Fatal(err)
	}
	pfs, err := features.Extract(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(pfs) < 2 {
		t.Fatalf("fixture has %d eligible players, want at least 2", len(pfs))
	}
	for _, pf := range pfs {
		fp, err := Enroll([]PlayerGame{{Vector: pf.Vector, Race: pf.Race}}, Meta{Label: pf.Name})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Add(fp); err != nil {
			t.Fatal(err)
		}
	}

	// The registry names the probe's own account after the probe's own
	// player, so exactly one candidate agrees and the rest disagree.
	reg := newTestRegistry(t, `{"version":1,"generated":"2026-09-06","accounts":[
	  {"name":`+jsonString(pfs[0].Name)+`,"aurora_id":1,"battle_tag":"bt",
	   "toons":[{"toon":`+jsonString(pfs[0].Name)+`,"gateway":30}],
	   "source":"test","last_verified":"2026-09-06"}]}`)

	probe := []PlayerGame{{Vector: pfs[0].Vector, Race: pfs[0].Race, Toon: pfs[0].Name}}
	plain, err := MatchMany(probe, db, WithMinZ(-100))
	if err != nil {
		t.Fatal(err)
	}
	withReg, err := MatchMany(probe, db, WithMinZ(-100), WithRegistry(reg))
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != len(withReg) {
		t.Fatalf("result counts differ: %d vs %d", len(plain), len(withReg))
	}
	for i := range plain {
		if plain[i].Label != withReg[i].Label || plain[i].Z != withReg[i].Z ||
			plain[i].Cosine != withReg[i].Cosine || plain[i].SearchFPR != withReg[i].SearchFPR {
			t.Fatalf("WithRegistry changed result %d: %+v vs %+v", i, plain[i], withReg[i])
		}
		if plain[i].Registry != nil {
			t.Fatalf("result %d carries an opinion without WithRegistry", i)
		}
	}

	// The opinion rides along on every candidate so each result can be read
	// on its own, and Agrees is the only part that varies between them.
	var sawAgree, sawDisagree bool
	for _, m := range withReg {
		if m.Registry == nil {
			t.Fatalf("candidate %q has no opinion", m.Label)
		}
		if m.Registry.Name != pfs[0].Name || m.Registry.Toon != pfs[0].Name {
			t.Fatalf("candidate %q opinion = %+v", m.Label, m.Registry)
		}
		if m.Registry.Agrees != strings.EqualFold(m.Label, pfs[0].Name) {
			t.Fatalf("candidate %q reported Agrees=%v", m.Label, m.Registry.Agrees)
		}
		if m.Registry.Agrees {
			sawAgree = true
		} else {
			sawDisagree = true
		}
	}
	if !sawAgree || !sawDisagree {
		t.Fatalf("expected both an agreeing and a disagreeing candidate (agree=%v disagree=%v)", sawAgree, sawDisagree)
	}
}

// jsonString quotes a value for embedding in a literal test document.
func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestMatchResultOmitsRegistryFromJSONWhenAbsent(t *testing.T) {
	// Consumers of the JSON shape must not see a null registry field on the
	// default, registry-free path.
	data, err := json.Marshal(MatchResult{Label: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "registry") {
		t.Fatalf("MatchResult JSON mentions registry when absent: %s", data)
	}
	data, err = json.Marshal(MatchResult{Label: "x", Registry: &RegistryOpinion{Name: "dummy", Agrees: true}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"registry"`) || !strings.Contains(string(data), `"agrees":true`) {
		t.Fatalf("MatchResult JSON = %s", data)
	}
}

func TestPrimaryToonPrefersTheActiveAlt(t *testing.T) {
	// Harvest targeting depends on this: the live alt is the one worth
	// pulling games from, and it is routinely not the primary handle.
	a := RegistryAccount{Toons: []RegistryToon{
		{Toon: "idle", Gateway: 30},
		{Toon: "live", Gateway: 11, GamesLastWeek: 18},
	}}
	got, ok := a.PrimaryToon()
	if !ok || got.Toon != "live" {
		t.Fatalf("PrimaryToon = %+v, %v", got, ok)
	}
	if _, ok := (RegistryAccount{}).PrimaryToon(); ok {
		t.Fatal("PrimaryToon claimed a toon on an account with none")
	}
}

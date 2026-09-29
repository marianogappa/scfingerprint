//go:build js && wasm

// Command scfingerprint-web exposes the library to the browser as WebAssembly,
// so the static page in web/ can analyse replays without uploading them.
//
// Every function takes and returns JSON strings: vectors round-trip through
// the page, which keeps this side stateless.
package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"syscall/js"

	"github.com/icza/screp/repparser"
	scfingerprint "github.com/marianogappa/scfingerprint"
	"github.com/marianogappa/scfingerprint/internal/verdict"
)

// minGamesPerSide is the public page's floor. A single game can reach a lead
// against a crowded corner of style space, and a stranger reading "you play
// like Soma" off one lead is exactly the misreading the page must not invite.
const minGamesPerSide = verdict.MinGamesStrong

const minZ = 2.0

var db *scfingerprint.Dataset

func main() {
	var err error
	db, err = scfingerprint.BuiltinDataset(scfingerprint.ConfidenceHigh)
	if err != nil {
		js.Global().Set("scfLoadError", err.Error())
		return
	}
	js.Global().Set("scfInfo", js.FuncOf(wrap(func([]js.Value) (any, error) { return info() })))
	js.Global().Set("scfParse", js.FuncOf(wrap(parse)))
	js.Global().Set("scfIdentify", js.FuncOf(wrap(identifyJS)))
	js.Global().Set("scfSame", js.FuncOf(wrap(sameJS)))
	js.Global().Call("scfReady")
	select {}
}

// wrap turns a Go handler into a JS function returning a JSON string, with
// failures (including parser panics on malformed replays) as {"error": ...}.
func wrap(fn func([]js.Value) (any, error)) func(js.Value, []js.Value) any {
	return func(_ js.Value, args []js.Value) (ret any) {
		defer func() {
			if r := recover(); r != nil {
				ret = errJSON(fmt.Errorf("internal error: %v", r))
			}
		}()
		out, err := fn(args)
		if err != nil {
			return errJSON(err)
		}
		b, err := json.Marshal(out)
		if err != nil {
			return errJSON(err)
		}
		return string(b)
	}
}

func errJSON(err error) string {
	b, _ := json.Marshal(map[string]string{"error": err.Error()})
	return string(b)
}

type infoOut struct {
	Model          string   `json:"model"`
	FeatureVersion int      `json:"feature_version"`
	CatalogSize    int      `json:"catalog_size"`
	Labels         []string `json:"labels"`
	MinGames       int      `json:"min_games"`
	Synthetic      bool     `json:"synthetic"`
}

func info() (any, error) {
	tag, err := scfingerprint.ModelTag()
	if err != nil {
		return nil, err
	}
	return infoOut{
		Model:          tag,
		FeatureVersion: scfingerprint.FeatureVersion(),
		CatalogSize:    db.Len(),
		Labels:         db.Labels(),
		MinGames:       minGamesPerSide,
		Synthetic:      db.ModelIsSynthetic(),
	}, nil
}

type parsedPlayer struct {
	ID     byte      `json:"id"`
	Name   string    `json:"name"`
	Race   string    `json:"race"`
	Vector []float64 `json:"vector"`
}

type parseOut struct {
	Map     string         `json:"map"`
	Date    string         `json:"date"`
	Seconds int            `json:"seconds"`
	Humans  []string       `json:"humans"`
	GameKey string         `json:"game_key"`
	Players []parsedPlayer `json:"players"`
}

// parse reads one replay's bytes. Players holds only those with enough play
// to fingerprint; Humans holds everyone who played, so the page can tell a
// team game from a 1v1 whose loser left early.
func parse(args []js.Value) (any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("parse wants the replay bytes")
	}
	data := make([]byte, args[0].Get("length").Int())
	js.CopyBytesToGo(data, args[0])

	r, err := repparser.ParseConfig(data, repparser.Config{Commands: true})
	if err != nil {
		return nil, fmt.Errorf("not a readable replay: %w", err)
	}
	vecs, err := scfingerprint.Extract(r)
	if err != nil {
		return nil, err
	}

	out := parseOut{
		Map:     stripColors(r.Header.Map),
		Date:    r.Header.StartTime.UTC().Format("2006-01-02"),
		Seconds: int(r.Header.Duration().Seconds()),
		Players: []parsedPlayer{},
	}
	for _, p := range r.Header.Players {
		if p.Observer || p.Type == nil || p.Type.Name != "Human" {
			continue
		}
		out.Humans = append(out.Humans, p.Name)
	}
	// Both players often save the same game; keying on start time and
	// participants lets the page count it once however many copies arrive.
	out.GameKey = fmt.Sprintf("%d|%s|%v", r.Header.StartTime.Unix(), r.Header.Map, out.Humans)
	for _, v := range vecs {
		out.Players = append(out.Players, parsedPlayer{ID: v.PlayerID, Name: v.Name, Race: v.Race, Vector: v.Vector})
	}
	return out, nil
}

// stripColors drops the control bytes StarCraft uses for in-game text colour.
func stripColors(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 {
			return -1
		}
		return r
	}, s)
}

type gameIn struct {
	Vector []float64 `json:"vector"`
	Race   string    `json:"race"`
}

type candidate struct {
	Label      string  `json:"label"`
	Liquipedia string  `json:"liquipedia,omitempty"`
	Z          float64 `json:"z"`
	SearchFPR  float64 `json:"search_fpr"`
	ClearsBar  bool    `json:"clears_bar"`
}

type identifyOut struct {
	Verdict    string      `json:"verdict"`
	Reason     string      `json:"reason,omitempty"`
	Games      int         `json:"games"`
	Candidates []candidate `json:"candidates"`
}

// Why a result stopped at lead, so the page can explain it without naming
// the near miss.
const (
	reasonBar    = "bar"    // the closest identity's crowded style demands more
	reasonMargin = "margin" // the top candidates are too close to call
)

func decodeGames(s string) ([]scfingerprint.PlayerGame, error) {
	var in []gameIn
	if err := json.Unmarshal([]byte(s), &in); err != nil {
		return nil, err
	}
	if len(in) < minGamesPerSide {
		return nil, fmt.Errorf("need at least %d games, got %d", minGamesPerSide, len(in))
	}
	games := make([]scfingerprint.PlayerGame, len(in))
	for i, g := range in {
		games[i] = scfingerprint.PlayerGame{Vector: g.Vector, Race: g.Race}
	}
	return games, nil
}

func identify(games []scfingerprint.PlayerGame) (identifyOut, error) {
	matches, err := scfingerprint.MatchMany(games, db, scfingerprint.WithMinZ(minZ))
	if err != nil {
		return identifyOut{}, err
	}
	out := identifyOut{Verdict: verdict.Match(matches, minZ), Games: len(games), Candidates: []candidate{}}
	if out.Verdict == verdict.Lead {
		out.Reason = reasonMargin
		if !matches[0].ClearsIdentityBar {
			out.Reason = reasonBar
		}
	}
	for _, m := range matches {
		out.Candidates = append(out.Candidates, candidate{
			Label:      m.Label,
			Liquipedia: m.Liquipedia,
			Z:          m.Z,
			SearchFPR:  m.SearchFPR,
			ClearsBar:  m.ClearsIdentityBar,
		})
	}
	return out, nil
}

func identifyJS(args []js.Value) (any, error) {
	games, err := decodeGames(args[0].String())
	if err != nil {
		return nil, err
	}
	return identify(games)
}

type sameOut struct {
	Verdict string      `json:"verdict"`
	Z       float64     `json:"z"`
	FPR     float64     `json:"fpr"`
	Games   int         `json:"games"`
	A       identifyOut `json:"a"`
	B       identifyOut `json:"b"`
	// Both pools the two sets, present only when they are the same player:
	// then all the games are evidence about one person, and the catalog
	// check should get to use all of them.
	Both *identifyOut `json:"both,omitempty"`
}

func sameJS(args []js.Value) (any, error) {
	a, err := decodeGames(args[0].String())
	if err != nil {
		return nil, fmt.Errorf("set A: %w", err)
	}
	b, err := decodeGames(args[1].String())
	if err != nil {
		return nil, fmt.Errorf("set B: %w", err)
	}
	v, err := scfingerprint.Same(a, b)
	if err != nil {
		return nil, err
	}
	out := sameOut{Verdict: verdict.Same(v.FPR, v.EvidenceN), Z: v.Z, FPR: v.FPR, Games: v.EvidenceN}
	if out.A, err = identify(a); err != nil {
		return nil, err
	}
	if out.B, err = identify(b); err != nil {
		return nil, err
	}
	if out.Verdict == verdict.Strong {
		both, err := identify(append(append([]scfingerprint.PlayerGame{}, a...), b...))
		if err != nil {
			return nil, err
		}
		out.Both = &both
	}
	return out, nil
}

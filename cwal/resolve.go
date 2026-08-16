package cwal

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	defaultSupabaseURL = "https://xmploueumzkrdvapbyfs.supabase.co/rest/v1"
	handlesView        = "player_other_handles_view"
)

// Resolver fetches current toons from the cwal.gg Supabase backend.
type Resolver struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// NewResolver creates a Resolver with the given Supabase anon API key.
func NewResolver(apiKey string) *Resolver {
	return &Resolver{
		apiKey:  apiKey,
		baseURL: defaultSupabaseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// ResolveToons fetches the current handles for an aurora_id from the cwal.gg
// Supabase API.
func (r *Resolver) ResolveToons(auroraID int64) ([]Toon, error) {
	url := fmt.Sprintf("%s/%s?select=*&battlenet_account=eq.%d", r.baseURL, handlesView, auroraID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("apikey", r.apiKey)
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cwal: resolving aurora_id %d: %w", auroraID, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("cwal: reading response for aurora_id %d: %w", auroraID, err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("cwal: API returned %d for aurora_id %d: %s", resp.StatusCode, auroraID, body)
	}

	var rows []struct {
		Alias   string `json:"alias"`
		Gateway int    `json:"gateway"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("cwal: parsing response for aurora_id %d: %w", auroraID, err)
	}

	toons := make([]Toon, len(rows))
	for i, row := range rows {
		toons[i] = Toon{Handle: row.Alias, Gateway: row.Gateway}
	}
	return toons, nil
}

// ResolveAll fetches toons for every account in the entries, with a rate
// limit delay between requests.
func (r *Resolver) ResolveAll(entries []Entry, delay time.Duration) ([]ResolvedEntry, error) {
	resolved := make([]ResolvedEntry, len(entries))
	for i, e := range entries {
		resolved[i] = ResolvedEntry{Entry: e, Toons: map[int64][]Toon{}}
		for _, acct := range e.Accounts {
			toons, err := r.ResolveToons(acct.AuroraID)
			if err != nil {
				return nil, err
			}
			if len(toons) > 0 {
				resolved[i].Toons[acct.AuroraID] = toons
			}
			if delay > 0 {
				time.Sleep(delay)
			}
		}
	}
	return resolved, nil
}

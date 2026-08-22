package cwal

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultBaseURL = "https://api.aws.cwal.gg"

// Resolver fetches current toons from the cwal.gg API.
type Resolver struct {
	baseURL    string
	httpClient *http.Client
}

// NewResolver creates a Resolver pointed at the cwal.gg API. The new API
// requires no authentication — only browser-like headers.
func NewResolver() *Resolver {
	return &Resolver{
		baseURL: defaultBaseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// ResolveToons fetches the current handles for an aurora_id.
func (r *Resolver) ResolveToons(auroraID int64) ([]Toon, error) {
	url := fmt.Sprintf("%s/api/account/%d/handles", r.baseURL, auroraID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
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

	var envelope struct {
		Handles []struct {
			Toon       string `json:"toon,omitempty"`
			Gateway    int    `json:"gateway"`
			BattleTag  string `json:"battleTag,omitempty"`
			AuroraID   int64  `json:"auroraId"`
			ProName    string `json:"proName,omitempty"`
			Race       string `json:"race,omitempty"`
			Rating     int    `json:"rating,omitempty"`
			LastUpdate int64  `json:"lastUpdated,omitempty"`
		} `json:"handles"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("cwal: parsing response for aurora_id %d: %w", auroraID, err)
	}

	toons := make([]Toon, 0, len(envelope.Handles))
	for _, h := range envelope.Handles {
		handle := h.Toon
		if handle == "" {
			handle = h.BattleTag
		}
		if handle == "" {
			continue
		}
		toons = append(toons, Toon{
			Handle:  handle,
			Gateway: h.Gateway,
		})
	}
	return toons, nil
}

// FetchPros fetches the full pro registry from the cwal.gg API.
func (r *Resolver) FetchPros() ([]Entry, error) {
	url := r.baseURL + "/api/pros"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cwal: fetching pros: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("cwal: reading pros response: %w", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("cwal: pros API returned %d: %s", resp.StatusCode, body)
	}

	var envelope struct {
		Pros map[string][]struct {
			AuroraID  int64  `json:"auroraId"`
			BattleTag string `json:"battleTag"`
		} `json:"pros"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("cwal: parsing pros: %w", err)
	}

	entries := make([]Entry, 0, len(envelope.Pros))
	for name, accts := range envelope.Pros {
		e := Entry{Nickname: name}
		for _, a := range accts {
			e.Accounts = append(e.Accounts, Account{
				AuroraID:  a.AuroraID,
				BattleTag: a.BattleTag,
			})
		}
		entries = append(entries, e)
	}
	return entries, nil
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

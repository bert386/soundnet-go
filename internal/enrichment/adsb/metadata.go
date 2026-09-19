package adsb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// AircraftInfo is what a hex code resolves to: the physical aircraft.
type AircraftInfo struct {
	Registration    string `json:"registration,omitempty"`
	TypeCode        string `json:"type_code,omitempty"` // ICAO type designator, e.g. B738
	TypeName        string `json:"type_name,omitempty"` // human-readable, e.g. 737NG 838/W
	Manufacturer    string `json:"manufacturer,omitempty"`
	Operator        string `json:"operator,omitempty"`
	OperatorCountry string `json:"operator_country,omitempty"`
}

// RouteInfo is what a callsign resolves to: the flight it is operating.
type RouteInfo struct {
	FlightICAO      string `json:"flight_icao,omitempty"` // e.g. QFA557
	FlightIATA      string `json:"flight_iata,omitempty"` // e.g. QF557 - the number on a ticket
	Airline         string `json:"airline,omitempty"`
	OriginIATA      string `json:"origin_iata,omitempty"`
	OriginICAO      string `json:"origin_icao,omitempty"`
	OriginName      string `json:"origin_name,omitempty"`
	DestinationIATA string `json:"destination_iata,omitempty"`
	DestinationICAO string `json:"destination_icao,omitempty"`
	DestinationName string `json:"destination_name,omitempty"`
}

// MetadataResolver turns the identifiers an aircraft broadcasts into a
// description of the aircraft and its flight.
//
// This is a separate interface from StateSource for a reason worth keeping in
// view: hex code and callsign are broadcast by the aircraft itself and are
// authoritative, whereas registration, type and route come from a community
// database and are a lookup. They are useful, but they are a different grade of
// evidence, and the stored record marks which is which.
type MetadataResolver interface {
	Aircraft(ctx context.Context, hex string) (*AircraftInfo, error)
	Route(ctx context.Context, callsign string) (*RouteInfo, error)
}

// ErrMetadataNotFound means the lookup succeeded but the identifier is unknown.
// Ordinary: military, private and newly registered aircraft are often absent.
var ErrMetadataNotFound = errors.New("adsb: metadata not found")

// AdsbdbResolver resolves aircraft and route metadata from adsbdb.com.
//
// The service is free and unauthenticated, which makes caching a courtesy as
// well as an optimisation. Aircraft data is effectively static - a registration
// does not change - so it is cached for the process lifetime. Routes are cached
// for a day, long enough to cover repeated overflights of the same scheduled
// service without going stale across a schedule change.
type AdsbdbResolver struct {
	HTTP    *http.Client
	BaseURL string

	// RouteTTL bounds how long a callsign-to-route mapping is reused.
	RouteTTL time.Duration

	mu       sync.RWMutex
	aircraft map[string]*AircraftInfo
	routes   map[string]routeCacheEntry
	notFound map[string]time.Time
}

type routeCacheEntry struct {
	info    *RouteInfo
	fetched time.Time
}

// DefaultAdsbdbURL is the public API root.
const DefaultAdsbdbURL = "https://api.adsbdb.com/v0"

// negativeTTL is how long an unknown identifier is remembered as unknown.
// Without it, every overflight of the same unlisted aircraft would re-query a
// free community service for an answer already known not to exist.
const negativeTTL = 6 * time.Hour

// NewAdsbdbResolver returns a resolver with caching enabled.
func NewAdsbdbResolver() *AdsbdbResolver {
	return &AdsbdbResolver{
		HTTP:     &http.Client{Timeout: 10 * time.Second},
		BaseURL:  DefaultAdsbdbURL,
		RouteTTL: 24 * time.Hour,
		aircraft: make(map[string]*AircraftInfo),
		routes:   make(map[string]routeCacheEntry),
		notFound: make(map[string]time.Time),
	}
}

// Aircraft resolves a hex code to the physical aircraft.
func (r *AdsbdbResolver) Aircraft(ctx context.Context, hex string) (*AircraftInfo, error) {
	hex = strings.ToLower(strings.TrimSpace(hex))
	if hex == "" {
		return nil, ErrMetadataNotFound
	}
	key := "a:" + hex

	r.mu.RLock()
	if info, ok := r.aircraft[hex]; ok {
		r.mu.RUnlock()
		return info, nil
	}
	if at, ok := r.notFound[key]; ok && time.Since(at) < negativeTTL {
		r.mu.RUnlock()
		return nil, ErrMetadataNotFound
	}
	r.mu.RUnlock()

	var payload struct {
		Response struct {
			Aircraft struct {
				Type            string `json:"type"`
				ICAOType        string `json:"icao_type"`
				Manufacturer    string `json:"manufacturer"`
				Registration    string `json:"registration"`
				RegisteredOwner string `json:"registered_owner"`
				OwnerCountry    string `json:"registered_owner_country_name"`
			} `json:"aircraft"`
		} `json:"response"`
	}
	if err := r.get(ctx, "/aircraft/"+hex, key, &payload); err != nil {
		return nil, err
	}

	a := payload.Response.Aircraft
	info := &AircraftInfo{
		Registration:    a.Registration,
		TypeCode:        a.ICAOType,
		TypeName:        a.Type,
		Manufacturer:    a.Manufacturer,
		Operator:        a.RegisteredOwner,
		OperatorCountry: a.OwnerCountry,
	}
	r.mu.Lock()
	r.aircraft[hex] = info
	r.mu.Unlock()
	return info, nil
}

// Route resolves a callsign to the flight it is operating.
func (r *AdsbdbResolver) Route(ctx context.Context, callsign string) (*RouteInfo, error) {
	callsign = strings.ToUpper(strings.TrimSpace(callsign))
	if callsign == "" {
		return nil, ErrMetadataNotFound
	}
	key := "r:" + callsign

	r.mu.RLock()
	if e, ok := r.routes[callsign]; ok && time.Since(e.fetched) < r.RouteTTL {
		r.mu.RUnlock()
		return e.info, nil
	}
	if at, ok := r.notFound[key]; ok && time.Since(at) < negativeTTL {
		r.mu.RUnlock()
		return nil, ErrMetadataNotFound
	}
	r.mu.RUnlock()

	var payload struct {
		Response struct {
			FlightRoute struct {
				CallsignICAO string `json:"callsign_icao"`
				CallsignIATA string `json:"callsign_iata"`
				Airline      struct {
					Name string `json:"name"`
				} `json:"airline"`
				Origin struct {
					IATA         string `json:"iata_code"`
					ICAO         string `json:"icao_code"`
					Municipality string `json:"municipality"`
				} `json:"origin"`
				Destination struct {
					IATA         string `json:"iata_code"`
					ICAO         string `json:"icao_code"`
					Municipality string `json:"municipality"`
				} `json:"destination"`
			} `json:"flightroute"`
		} `json:"response"`
	}
	if err := r.get(ctx, "/callsign/"+callsign, key, &payload); err != nil {
		return nil, err
	}

	fr := payload.Response.FlightRoute
	info := &RouteInfo{
		FlightICAO:      fr.CallsignICAO,
		FlightIATA:      fr.CallsignIATA,
		Airline:         fr.Airline.Name,
		OriginIATA:      fr.Origin.IATA,
		OriginICAO:      fr.Origin.ICAO,
		OriginName:      fr.Origin.Municipality,
		DestinationIATA: fr.Destination.IATA,
		DestinationICAO: fr.Destination.ICAO,
		DestinationName: fr.Destination.Municipality,
	}
	r.mu.Lock()
	r.routes[callsign] = routeCacheEntry{info: info, fetched: time.Now()}
	r.mu.Unlock()
	return info, nil
}

// get performs a request and decodes it, recording 404s as negative cache
// entries so an unknown identifier is not asked for repeatedly.
func (r *AdsbdbResolver) get(ctx context.Context, path, cacheKey string, out any) error {
	base := r.BaseURL
	if base == "" {
		base = DefaultAdsbdbURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, http.NoBody)
	if err != nil {
		return fmt.Errorf("adsb: build metadata request: %w", err)
	}
	client := r.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("adsb: metadata request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		// Military, private and newly registered aircraft are routinely absent.
		// Remembering that avoids re-querying a free service for an answer we
		// already know does not exist.
		r.mu.Lock()
		r.notFound[cacheKey] = time.Now()
		r.mu.Unlock()
		return ErrMetadataNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("adsb: metadata endpoint returned %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("adsb: decode metadata: %w", err)
	}
	return nil
}

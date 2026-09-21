package detections

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/bert386/soundnet-go/internal/datastore"
	"github.com/bert386/soundnet-go/internal/eventclass"
)

// SoundNet: filtering detections by event domain.
//
// The `category` query parameter names one or more event domains - aircraft,
// vehicle, alarm, impulse, tool, rail, watercraft, weather - or the aggregate
// "events", meaning all of them. It expands to the label names those domains
// are stored under and rides the existing Species filter, which the v2 store
// already resolves to label IDs and applies as `label_id IN (...)`.
//
// That is why this needs no datastore or repository change: the machinery for
// "restrict to these labels" exists, and a domain is just a named set of them.
//
// It matches on the TRUNCATED label name ("jet", not "jet_engine"), because
// that is what the datastore stores - see eventclass.StorageName. A test in
// internal/eventclass proves no truncated name spans two domains, which is the
// assumption that makes this sound.

// categoryEvents is the aggregate that means "anything SoundNet detects that a
// bird model would not". It is the default a user reaches for first, so it is
// worth having rather than making them tick eight boxes.
const categoryEvents = "events"

// expandCategory converts a category parameter into the label names to filter
// on. ok is false when the parameter names nothing recognisable, which the
// caller must treat as a bad request rather than silently ignore: quietly
// dropping an unknown filter would show the user every detection and let them
// believe it was the filtered set.
func expandCategory(category string) (names []string, ok bool) {
	category = strings.TrimSpace(category)
	if category == "" {
		return nil, false
	}

	var domains []eventclass.Domain
	for part := range strings.SplitSeq(category, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.EqualFold(part, categoryEvents) {
			domains = append(domains, eventclass.FilterableDomains()...)
			continue
		}
		d, valid := eventclass.ParseDomain(part)
		if !valid {
			return nil, false
		}
		domains = append(domains, d)
	}
	if len(domains) == 0 {
		return nil, false
	}

	names = eventclass.StorageNamesForDomains(domains)

	// Widened by the ambiguity table, because the class a detection was
	// recorded under is often not the domain it belongs to: most of this
	// station's aircraft arrive as `Thunder` or `Vehicle` and are only settled
	// afterwards by ADS-B. The extra rows this admits are removed again by
	// filterByResolvedDomain once each one's resolved domain is known.
	names = append(names, eventclass.StorageNamesAmbiguousWith(domains)...)

	// A recognised domain with no enabled classes would otherwise produce an
	// empty filter, which the store reads as "no restriction" - the opposite of
	// what the user asked for.
	return names, len(names) > 0
}

// requestedDomains parses the category parameter back into the domains it
// names, for the second half of the filter.
func requestedDomains(category string) map[string]struct{} {
	out := make(map[string]struct{})
	for part := range strings.SplitSeq(strings.TrimSpace(category), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.EqualFold(part, categoryEvents) {
			for _, d := range eventclass.FilterableDomains() {
				out[string(d)] = struct{}{}
			}
			continue
		}
		if d, valid := eventclass.ParseDomain(part); valid {
			out[string(d)] = struct{}{}
		}
	}
	return out
}

// filterByResolvedDomain keeps only the detections that actually belong to the
// requested domains, judged by what an authority settled rather than by the
// class name.
//
// This is the half of the category filter SQL cannot do. The resolved domain
// lives in a JSON payload in one of this fork's own tables, and the two
// datastores upstream supports do not even agree on what the detections table
// is called, so there is no join to write. The page is annotated first and
// filtered second.
//
// The consequence, stated plainly because it is visible: the total reported
// beside the list is the count the database returned for the widened query, so
// it is an upper bound. The rows are right; the total can read high.
func filterByResolvedDomain(detections []DetectionResponse, category string) []DetectionResponse {
	if category == "" {
		return detections
	}
	wanted := requestedDomains(category)
	if len(wanted) == 0 {
		return detections
	}

	kept := make([]DetectionResponse, 0, len(detections))
	for i := range detections {
		domain := detections[i].ResolvedDomain
		if domain == "" {
			// Nothing identified it, so the acoustic class stands. This has to
			// be checked rather than assumed: the query was widened to the
			// classes this domain is ambiguous with, so an unidentified
			// `Thunder` row reaches an aircraft request and would otherwise be
			// waved through on the strength of a widening that exists only to
			// find the identified ones.
			class, found := eventclass.Resolve(detections[i].ScientificName)
			if !found {
				continue
			}
			domain = string(class.Domain)
		}
		if _, hit := wanted[domain]; hit {
			kept = append(kept, detections[i])
		}
	}
	return kept
}

// applyCategoryFilter narrows filters to an event domain.
//
// An explicit species filter wins: it is strictly more specific than a domain,
// and intersecting the two here would need set logic the store's single
// label-ID list cannot express.
func applyCategoryFilter(filters *datastore.AdvancedSearchFilters, category string) bool {
	if category == "" {
		return true
	}
	names, ok := expandCategory(category)
	if !ok {
		return false
	}
	if len(filters.Species) > 0 {
		return true
	}
	filters.Species = names
	return true
}

// CategoryOptions describes the categories a client may filter by, so the UI can
// build its control from the taxonomy rather than hardcoding a list that drifts.
type CategoryOptions struct {
	Categories []CategoryOption `json:"categories"`
}

// CategoryOption is one selectable category.
type CategoryOption struct {
	ID string `json:"id"`
	// Labels is how many distinct stored names it matches, which tells the UI
	// whether a category is worth offering at all.
	Labels int `json:"labels"`
}

// GetDetectionCategories serves the selectable categories.
//
// Taxonomy-derived rather than a list the frontend keeps in step by hand: adding
// a domain in internal/eventclass should make it filterable without a second
// edit somewhere that can be forgotten.
func (c *Handler) GetDetectionCategories(ctx echo.Context) error {
	return ctx.JSON(http.StatusOK, categoryOptions())
}

// categoryOptions lists the selectable categories, taxonomy-derived.
func categoryOptions() CategoryOptions {
	domains := eventclass.FilterableDomains()
	out := CategoryOptions{Categories: make([]CategoryOption, 0, len(domains)+1)}
	out.Categories = append(out.Categories, CategoryOption{
		ID:     categoryEvents,
		Labels: len(eventclass.StorageNamesForDomains(domains)),
	})
	for _, d := range domains {
		out.Categories = append(out.Categories, CategoryOption{
			ID:     string(d),
			Labels: len(eventclass.StorageNames(d)),
		})
	}
	return out
}

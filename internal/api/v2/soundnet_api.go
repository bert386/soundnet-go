package api

// SoundNet API.
//
// Exposes the diagnostics and identity a detection carries, which are otherwise
// only visible by opening the database. Registered from a new file so the fork
// adds one line to the upstream route table.
//
// This is deliberately a read-only inspection surface rather than the M7 user
// interface. It exists so the data can be checked as soon as it starts arriving,
// which is worth more than a polished view of numbers nobody has verified yet.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	"github.com/bert386/soundnet-go/internal/eventclass"
	"github.com/bert386/soundnet-go/internal/eventrecord"
)

// soundNetErrKey is the JSON key for an error message, named so the repeated
// literal does not collide with the logging constant of the same value.
const soundNetErrKey = "error"

// initSoundNetRoutes registers the SoundNet inspection endpoints.
func (c *Controller) initSoundNetRoutes() {
	g := c.Group.Group("/soundnet")
	g.GET("/detections/:id", c.GetSoundNetDetection)
	g.GET("/detections", c.ListSoundNetDetections)
	g.GET("/taxonomy", c.GetSoundNetTaxonomy)
	// Corrections change data, so they sit behind the same auth as other writes.
	g.POST("/detections/:id/correction", c.PostSoundNetCorrection, c.AuthMiddleware)
	g.GET("/confusions", c.GetSoundNetConfusions)
	g.GET("/training-export", c.GetSoundNetTrainingExport)
	g.GET("/threshold-preview", c.GetSoundNetThresholdPreview)
}

// soundNetDetectionResponse is what a detection looks like through this lens.
type soundNetDetectionResponse struct {
	DetectionID uint   `json:"detectionId"`
	Domain      string `json:"domain"`

	// Diagnosable and Enrichable explain the absence of data rather than leaving
	// a caller to guess. "No diagnostics" because the domain has nothing to
	// measure is a completely different statement from "diagnostics failed", and
	// a UI that cannot tell them apart will mislead.
	Diagnosable bool `json:"diagnosable"`
	Enrichable  bool `json:"enrichable"`

	// CandidateDomains is every domain the label could belong to, its own
	// first. Longer than one entry only for a label that does not determine
	// its own domain, and present so that an aircraft identity on a detection
	// recorded as a vehicle reads as a deliberate question rather than a bug.
	CandidateDomains []string `json:"candidateDomains,omitempty"`

	Diagnostics *soundNetDiagnostics `json:"diagnostics,omitempty"`
	Enrichment  []soundNetEnrichment `json:"enrichment,omitempty"`
	Correction  *soundNetCorrection  `json:"correction,omitempty"`

	// ReviewState is derived, not stored. See internal/eventrecord.
	ReviewState string `json:"reviewState"`

	// Note carries the reason a section is empty, when there is one worth saying.
	Note string `json:"note,omitempty"`
}

type soundNetDiagnostics struct {
	SchemaVersion int    `json:"schemaVersion"`
	ComputeMs     int64  `json:"computeMs"`
	Payload       any    `json:"payload"`
	ComputedAt    string `json:"computedAt"`
}

type soundNetEnrichment struct {
	Provider        string  `json:"provider"`
	Source          string  `json:"source"`
	Confidence      float64 `json:"confidence"`
	LagCorrectionMs int64   `json:"lagCorrectionMs"`
	Attributes      any     `json:"attributes"`
	ResolvedAt      string  `json:"resolvedAt"`
}

type soundNetCorrection struct {
	OriginalLabelID  uint   `json:"originalLabelId"`
	CorrectedLabelID uint   `json:"correctedLabelId"`
	Note             string `json:"note,omitempty"`
}

// GetSoundNetDetection returns everything SoundNet knows about one detection.
func (c *Controller) GetSoundNetDetection(ctx echo.Context) error {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{
			soundNetErrKey: "detection id must be numeric",
		})
	}
	detectionID := uint(id)

	resp := soundNetDetectionResponse{DetectionID: detectionID, ReviewState: string(eventrecord.ReviewUnreviewed)}

	if c.DS == nil {
		return ctx.JSON(http.StatusServiceUnavailable, map[string]string{soundNetErrKey: "datastore unavailable"})
	}

	err = c.DS.Transaction(func(tx *gorm.DB) error {
		store := eventrecord.NewStore(tx)

		var payload any
		version, derr := store.GetDiagnostics(detectionID, &payload)
		switch {
		case derr == nil:
			var rec eventrecord.Diagnostics
			if e := tx.Where("detection_id = ?", detectionID).First(&rec).Error; e == nil {
				resp.Diagnostics = &soundNetDiagnostics{
					SchemaVersion: version,
					ComputeMs:     rec.ComputeMs,
					Payload:       payload,
					ComputedAt:    rec.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
				}
			}
		case !errors.Is(derr, eventrecord.ErrNotFound):
			return derr
		}

		var enrichRows []eventrecord.Enrichment
		if e := tx.Where("detection_id = ?", detectionID).Find(&enrichRows).Error; e != nil {
			return e
		}
		for i := range enrichRows {
			var attrs any
			_ = decodeJSON(enrichRows[i].Payload, &attrs)
			resp.Enrichment = append(resp.Enrichment, soundNetEnrichment{
				Provider:        enrichRows[i].Provider,
				Source:          enrichRows[i].Source,
				Confidence:      enrichRows[i].Confidence,
				LagCorrectionMs: enrichRows[i].LagCorrectionMs,
				Attributes:      attrs,
				ResolvedAt:      enrichRows[i].CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			})
		}

		if corr, cerr := store.GetCorrection(detectionID); cerr == nil {
			resp.Correction = &soundNetCorrection{
				OriginalLabelID:  corr.OriginalLabelID,
				CorrectedLabelID: corr.CorrectedLabelID,
				Note:             corr.Note,
			}
			resp.ReviewState = string(eventrecord.ReviewCorrected)
		} else if !errors.Is(cerr, eventrecord.ErrNotFound) {
			return cerr
		}
		return nil
	})
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, map[string]string{soundNetErrKey: err.Error()})
	}

	// Resolve the domain so the response can explain what was and was not run.
	if label := c.soundNetLabelFor(detectionID); label != "" {
		class, _ := eventclass.Resolve(label)
		resp.Domain = string(class.Domain)
		resp.Diagnosable = class.Domain.Diagnosable()
		// class.Enrichable rather than class.Domain.Enrichable: for an ambiguous
		// label the honest answer is that an authority might have something to
		// say, because one was in fact asked.
		resp.Enrichable = class.Enrichable()
		if candidates := class.CandidateDomains(); len(candidates) > 1 {
			resp.CandidateDomains = make([]string, 0, len(candidates))
			for _, d := range candidates {
				resp.CandidateDomains = append(resp.CandidateDomains, string(d))
			}
		}

		switch {
		case resp.Diagnostics == nil && !resp.Diagnosable:
			resp.Note = "no diagnostics: this domain has nothing measurable - no transients to count, no pass-by geometry, no decay worth timing"
		case resp.Diagnostics == nil:
			resp.Note = "no diagnostics recorded; the layer may be disabled in configuration"
		case len(resp.Enrichment) == 0 && !resp.Enrichable:
			resp.Note = "no identity: no authoritative external source exists for this kind of event"
		}
	}

	return ctx.JSON(http.StatusOK, resp)
}

// ListSoundNetDetections lists detections that carry SoundNet data.
//
// Useful precisely because it is usually short: it answers "has anything been
// measured yet" without scrolling the whole detection list.
func (c *Controller) ListSoundNetDetections(ctx echo.Context) error {
	if c.DS == nil {
		return ctx.JSON(http.StatusServiceUnavailable, map[string]string{soundNetErrKey: "datastore unavailable"})
	}
	limit := 50
	if v := ctx.QueryParam("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	type row struct {
		DetectionID uint   `json:"detectionId"`
		Kind        string `json:"kind"`
		ComputeMs   int64  `json:"computeMs,omitempty"`
		Provider    string `json:"provider,omitempty"`
		CreatedAt   string `json:"createdAt"`
	}
	out := []row{}

	err := c.DS.Transaction(func(tx *gorm.DB) error {
		var diags []eventrecord.Diagnostics
		if e := tx.Order("id desc").Limit(limit).Find(&diags).Error; e != nil {
			return e
		}
		for i := range diags {
			out = append(out, row{
				DetectionID: diags[i].DetectionID,
				Kind:        "diagnostics",
				ComputeMs:   diags[i].ComputeMs,
				CreatedAt:   diags[i].CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			})
		}
		var enr []eventrecord.Enrichment
		if e := tx.Order("id desc").Limit(limit).Find(&enr).Error; e != nil {
			return e
		}
		for i := range enr {
			out = append(out, row{
				DetectionID: enr[i].DetectionID,
				Kind:        "enrichment",
				Provider:    enr[i].Provider,
				CreatedAt:   enr[i].CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			})
		}
		return nil
	})
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, map[string]string{soundNetErrKey: err.Error()})
	}
	return ctx.JSON(http.StatusOK, map[string]any{"data": out, "count": len(out)})
}

// GetSoundNetTaxonomy returns the event taxonomy: which classes map to which
// domain, what is enabled, and what each domain supports.
//
// Exposed because "why did this detection get no diagnostics" is the question
// this data answers, and answering it from the database means joining tables by
// hand.
func (c *Controller) GetSoundNetTaxonomy(ctx echo.Context) error {
	type classOut struct {
		Label          string `json:"label"`
		Domain         string `json:"domain"`
		DefaultEnabled bool   `json:"defaultEnabled"`
		AudioSetIndex  int    `json:"audioSetIndex"`

		// CandidateDomains is set only where the class's domain is a best reading
		// rather than a settled fact, so the taxonomy view shows which few labels
		// those are instead of leaving it to the source.
		CandidateDomains []string `json:"candidateDomains,omitempty"`
	}
	type domainOut struct {
		Domain      string     `json:"domain"`
		Diagnosable bool       `json:"diagnosable"`
		Enrichable  bool       `json:"enrichable"`
		Classes     []classOut `json:"classes"`
	}

	domains := make([]domainOut, 0, len(eventclass.AllDomains()))
	for _, d := range eventclass.AllDomains() {
		do := domainOut{
			Domain:      string(d),
			Diagnosable: d.Diagnosable(),
			Enrichable:  d.Enrichable(),
			Classes:     []classOut{},
		}
		for _, cl := range eventclass.InDomain(d) {
			out := classOut{
				Label:          cl.Label,
				Domain:         string(cl.Domain),
				DefaultEnabled: cl.DefaultEnabled,
				AudioSetIndex:  cl.AudioSetIndex,
			}
			if candidates := cl.CandidateDomains(); len(candidates) > 1 {
				for _, cd := range candidates {
					out.CandidateDomains = append(out.CandidateDomains, string(cd))
				}
			}
			do.Classes = append(do.Classes, out)
		}
		domains = append(domains, do)
	}
	return ctx.JSON(http.StatusOK, map[string]any{"domains": domains})
}

// soundNetLabelFor resolves a detection's label so the domain can be reported.
// Returns "" when it cannot be resolved, in which case the response simply omits
// the domain rather than guessing at one.
func (c *Controller) soundNetLabelFor(detectionID uint) string {
	if c.DS == nil {
		return ""
	}
	var label string
	_ = c.DS.Transaction(func(tx *gorm.DB) error {
		var name string
		if e := tx.Raw(
			"select l.scientific_name from detections d join labels l on l.id = d.label_id where d.id = ?",
			detectionID,
		).Scan(&name).Error; e == nil {
			label = name
		}
		return nil
	})
	return label
}

// decodeJSON unmarshals a stored payload, tolerating an empty one.
func decodeJSON(raw []byte, out any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// correctionRequest is the body of a reclassification.
type correctionRequest struct {
	// CorrectedLabelID is the label the operator says is right. Required: a
	// correction without an answer is just a false positive, which upstream's
	// review endpoint already records.
	CorrectedLabelID uint `json:"correctedLabelId"`

	// Note is optional free text explaining the correction. Worth capturing:
	// "distant, mostly masked by traffic" tells a future reader why a confusing
	// example was labelled the way it was.
	Note string `json:"note"`
}

// PostSoundNetCorrection records an operator reclassifying a detection.
//
// Separate from upstream's review endpoint because it records something that
// endpoint cannot: not merely that the model was wrong, but what the right
// answer was. That difference is what makes a correction useful for retraining -
// a confusion pair teaches far more than a rejection.
func (c *Controller) PostSoundNetCorrection(ctx echo.Context) error {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{soundNetErrKey: "detection id must be numeric"})
	}
	detectionID := uint(id)

	var req correctionRequest
	if bindErr := ctx.Bind(&req); bindErr != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{soundNetErrKey: "invalid request body"})
	}
	if req.CorrectedLabelID == 0 {
		return ctx.JSON(http.StatusBadRequest, map[string]string{
			soundNetErrKey: "correctedLabelId is required; to record that a detection is simply wrong, use the review endpoint instead",
		})
	}
	if c.DS == nil {
		return ctx.JSON(http.StatusServiceUnavailable, map[string]string{soundNetErrKey: "datastore unavailable"})
	}

	var originalLabelID uint
	err = c.DS.Transaction(func(tx *gorm.DB) error {
		// The original label is captured at correction time so the confusion
		// pair survives even if the detection is later re-pointed. A bare
		// corrected answer discards half of what makes this useful.
		if e := tx.Raw("select label_id from detections where id = ?", detectionID).
			Scan(&originalLabelID).Error; e != nil {
			return e
		}
		if originalLabelID == 0 {
			return errDetectionNotFound
		}
		return eventrecord.NewStore(tx).PutCorrection(detectionID, originalLabelID, req.CorrectedLabelID, req.Note)
	})
	switch {
	case errors.Is(err, errDetectionNotFound):
		return ctx.JSON(http.StatusNotFound, map[string]string{soundNetErrKey: "detection not found"})
	case err != nil:
		return ctx.JSON(http.StatusBadRequest, map[string]string{soundNetErrKey: err.Error()})
	}

	return ctx.JSON(http.StatusOK, map[string]any{
		"detectionId":      detectionID,
		"originalLabelId":  originalLabelID,
		"correctedLabelId": req.CorrectedLabelID,
		"reviewState":      string(eventrecord.ReviewCorrected),
	})
}

// errDetectionNotFound distinguishes a missing detection from a storage failure,
// so the caller gets 404 rather than a misleading 400.
var errDetectionNotFound = errors.New("detection not found")

// GetSoundNetConfusions returns detections whose class is one a single
// microphone cannot reliably separate from its neighbours.
//
// This is the paired-review surface. Gunshot versus vehicle backfire is the
// motivating case: both are short, loud and broadband, and the scope is explicit
// that the distinction is not recoverable from one microphone. What a human can
// do, given the audio and the context, is decide - and each decision becomes a
// labelled example for exactly the discrimination the model finds hardest.
func (c *Controller) GetSoundNetConfusions(ctx echo.Context) error {
	label := ctx.QueryParam("label")
	if label == "" {
		// Without a label there is no pair to review. Listing every ambiguous
		// class instead lets the UI offer a starting point.
		sets := map[string][]string{}
		for _, d := range eventclass.AllDomains() {
			for _, cl := range eventclass.InDomain(d) {
				if set := eventclass.ConfusionSet(cl.Label); len(set) > 0 {
					sets[cl.Label] = set
				}
			}
		}
		return ctx.JSON(http.StatusOK, map[string]any{"sets": sets})
	}

	set := eventclass.ConfusionSet(label)
	if len(set) == 0 {
		// Not an error: most classes have no genuine confusion, and saying so is
		// more useful than an empty list the caller has to interpret.
		return ctx.JSON(http.StatusOK, map[string]any{
			"label": label,
			"set":   []string{},
			"note":  "this class has no genuine confusion from a single microphone; paired review would not add information",
		})
	}
	return ctx.JSON(http.StatusOK, map[string]any{"label": label, "set": set})
}

// GetSoundNetTrainingExport describes the labelled corpus a retrain would use.
//
// A manifest rather than an archive. The clips already exist on disk, and
// copying gigabytes to describe them would be slow, duplicative and immediately
// stale. A manifest can also be regenerated cheaply as review continues.
func (c *Controller) GetSoundNetTrainingExport(ctx echo.Context) error {
	if c.DS == nil {
		return ctx.JSON(http.StatusServiceUnavailable, map[string]string{soundNetErrKey: "datastore unavailable"})
	}

	type example struct {
		DetectionID uint   `json:"detectionId"`
		Label       string `json:"label"`
		Origin      string `json:"origin"`
		ClipName    string `json:"clipName,omitempty"`
	}
	examples := []example{}
	counts := map[string]int{}

	err := c.DS.Transaction(func(tx *gorm.DB) error {
		// Corrections first. A corrected example is worth more than a confirmed
		// one: it marks a case the model got wrong, which is where the training
		// signal actually is.
		type correctedRow struct {
			DetectionID uint
			Label       string
			ClipName    string
		}
		var corrected []correctedRow
		if e := tx.Raw(`
			select c.detection_id as detection_id,
			       l.scientific_name as label,
			       coalesce(d.clip_name, '') as clip_name
			from soundnet_corrections c
			join labels l on l.id = c.corrected_label_id
			join detections d on d.id = c.detection_id
		`).Scan(&corrected).Error; e != nil {
			return e
		}
		for i := range corrected {
			examples = append(examples, example{
				DetectionID: corrected[i].DetectionID,
				Label:       corrected[i].Label,
				Origin:      "corrected",
				ClipName:    corrected[i].ClipName,
			})
			counts[corrected[i].Label]++
		}

		type confirmedRow struct {
			DetectionID uint
			Label       string
			ClipName    string
		}
		var confirmed []confirmedRow
		if e := tx.Raw(`
			select d.id as detection_id,
			       l.scientific_name as label,
			       coalesce(d.clip_name, '') as clip_name
			from detection_reviews r
			join detections d on d.id = r.detection_id
			join labels l on l.id = d.label_id
			where r.verified = 'correct'
			  and d.id not in (select detection_id from soundnet_corrections)
		`).Scan(&confirmed).Error; e != nil {
			// A missing review table is not fatal: corrections alone are a
			// usable corpus, and failing the whole export would hide them.
			return nil //nolint:nilerr // corrections alone still constitute a corpus
		}
		for i := range confirmed {
			examples = append(examples, example{
				DetectionID: confirmed[i].DetectionID,
				Label:       confirmed[i].Label,
				Origin:      "confirmed",
				ClipName:    confirmed[i].ClipName,
			})
			counts[confirmed[i].Label]++
		}
		return nil
	})
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, map[string]string{soundNetErrKey: err.Error()})
	}

	// Classes with too few examples to train on are reported rather than
	// silently included: a class with three examples will not produce a usable
	// head, and discovering that after a training run wastes an afternoon.
	const minUsable = 20
	thin := []string{}
	for label, n := range counts {
		if n < minUsable {
			thin = append(thin, fmt.Sprintf("%s (%d)", label, n))
		}
	}

	return ctx.JSON(http.StatusOK, map[string]any{
		"examples":    examples,
		"total":       len(examples),
		"perLabel":    counts,
		"thinClasses": thin,
		"minUsable":   minUsable,
		"clipsNote":   "clip files are referenced, not copied; they remain in the configured clip directory",
	})
}

// GetSoundNetThresholdPreview reports what a confidence threshold would have
// done to detections already recorded.
//
// Previewing against real history is the point. A threshold is otherwise tuned
// by changing a number, waiting a day and guessing at the difference - and by
// then the conditions have changed too. Replaying stored confidences answers
// "what would I have lost" immediately and exactly.
//
// It reports only what it can know. Detections filtered out before being stored
// are invisible here, so raising a threshold can be evaluated precisely while
// lowering one cannot: the recordings that would newly appear were never kept.
// The response says so rather than implying a symmetry that does not exist.
func (c *Controller) GetSoundNetThresholdPreview(ctx echo.Context) error {
	if c.DS == nil {
		return ctx.JSON(http.StatusServiceUnavailable, map[string]string{soundNetErrKey: "datastore unavailable"})
	}
	threshold := 0.7
	if v := ctx.QueryParam("threshold"); v != "" {
		parsed, err := strconv.ParseFloat(v, 64)
		if err != nil || parsed < 0 || parsed > 1 {
			return ctx.JSON(http.StatusBadRequest, map[string]string{
				soundNetErrKey: "threshold must be a number between 0 and 1",
			})
		}
		threshold = parsed
	}
	label := ctx.QueryParam("label")

	type bucket struct {
		Label  string  `json:"label"`
		Total  int     `json:"total"`
		Kept   int     `json:"kept"`
		Lost   int     `json:"lost"`
		Median float64 `json:"medianConfidence"`
	}
	buckets := map[string]*bucket{}
	var total, kept int

	err := c.DS.Transaction(func(tx *gorm.DB) error {
		type row struct {
			Label      string
			Confidence float64
		}
		var rows []row
		q := `select l.scientific_name as label, d.confidence as confidence
		      from detections d join labels l on l.id = d.label_id`
		args := []any{}
		if label != "" {
			q += " where l.scientific_name = ?"
			args = append(args, label)
		}
		q += " order by d.id desc limit 5000"
		if e := tx.Raw(q, args...).Scan(&rows).Error; e != nil {
			return e
		}
		confidences := map[string][]float64{}
		for i := range rows {
			b, ok := buckets[rows[i].Label]
			if !ok {
				b = &bucket{Label: rows[i].Label}
				buckets[rows[i].Label] = b
			}
			b.Total++
			total++
			if rows[i].Confidence >= threshold {
				b.Kept++
				kept++
			} else {
				b.Lost++
			}
			confidences[rows[i].Label] = append(confidences[rows[i].Label], rows[i].Confidence)
		}
		for lbl, vals := range confidences {
			if len(vals) == 0 {
				continue
			}
			slices.Sort(vals)
			buckets[lbl].Median = vals[len(vals)/2]
		}
		return nil
	})
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, map[string]string{soundNetErrKey: err.Error()})
	}

	out := make([]bucket, 0, len(buckets))
	for _, b := range buckets {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Total > out[j].Total })

	return ctx.JSON(http.StatusOK, map[string]any{
		"threshold": threshold,
		"total":     total,
		"kept":      kept,
		"lost":      total - kept,
		"perLabel":  out,
		"caveat":    "counts cover detections already stored. Raising a threshold can be judged exactly; lowering one cannot, because detections below the current threshold were never recorded.",
	})
}

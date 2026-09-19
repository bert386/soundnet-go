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
	"net/http"
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
		class, _ := eventclass.Lookup(label)
		resp.Domain = string(class.Domain)
		resp.Diagnosable = class.Domain.Diagnosable()
		resp.Enrichable = class.Domain.Enrichable()

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
			do.Classes = append(do.Classes, classOut{
				Label:          cl.Label,
				Domain:         string(cl.Domain),
				DefaultEnabled: cl.DefaultEnabled,
				AudioSetIndex:  cl.AudioSetIndex,
			})
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

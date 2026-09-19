package analysis

import "github.com/bert386/soundnet-go/internal/errors"

// ErrAnalysisCanceled is returned when the analysis is canceled by the user
var ErrAnalysisCanceled = errors.NewStd("analysis canceled")

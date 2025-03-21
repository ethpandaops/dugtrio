package handlers

import (
	"net/http"

	"github.com/ethpandaops/dugtrio/frontend"
)

type SessionsPage struct {
	Sessions        []*SessionsPageSession `json:"sessions"`
	SessionCount    uint64                 `json:"session_count"`
	TotalRequests   uint64                 `json:"total_requests"`
	TotalValidators uint64                 `json:"total_validators"`
}

type SessionsPageSession struct {
	Index            int                                 `json:"index"`
	Key              string                              `json:"key"`
	FirstSeen        string                              `json:"first_seen"`
	LastSeen         string                              `json:"last_seen"`
	Requests         uint64                              `json:"requests"`
	Tokens           float64                             `json:"tokens"`
	ValidatorCount   uint64                              `json:"validator_count"`
	Target           string                              `json:"target"`
	ValidatorStats   []SessionsPageSessionValidatorStats `json:"validator_stats"`
	AggregatedRanges []ValidatorRange                    `json:"aggregated_ranges"`
}

type SessionsPageSessionValidatorStats struct {
	Start  uint64 `json:"start"`
	Length uint32 `json:"length"`
	Flag   uint8  `json:"flag"`
}

type ValidatorRange struct {
	Start uint64
	End   uint64
}

// Sessions will return the "sessions" page using a go template
func (fh *FrontendHandler) Sessions(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(frontend.LayoutTemplateFiles,
		"sessions/sessions.html",
	)

	var pageTemplate = frontend.GetTemplate(templateFiles...)
	data := frontend.InitPageData(w, r, "sessions", "/sessions", "Sessions", templateFiles)

	var pageError error
	data.Data, pageError = fh.getSessionsPageData()
	if pageError != nil {
		frontend.HandlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if frontend.HandleTemplateError(w, r, "sessions.go", "Sessions", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func (fh *FrontendHandler) aggregateValidatorRanges(stats []SessionsPageSessionValidatorStats) []ValidatorRange {
	if len(stats) == 0 {
		return nil
	}

	ranges := make([]ValidatorRange, 0)
	current := ValidatorRange{
		Start: stats[0].Start,
		End:   stats[0].Start + uint64(stats[0].Length) - 1,
	}

	for i := 1; i < len(stats); i++ {
		start := stats[i].Start
		end := start + uint64(stats[i].Length) - 1

		if start == current.End+1 {
			// Extend current range
			current.End = end
		} else {
			// Save current range and start new one
			ranges = append(ranges, current)
			current = ValidatorRange{Start: start, End: end}
		}
	}
	ranges = append(ranges, current)
	return ranges
}

func (fh *FrontendHandler) getSessionsPageData() (*SessionsPage, error) {
	pageData := &SessionsPage{
		Sessions: []*SessionsPageSession{},
	}

	// get sessions
	for index, session := range fh.proxy.GetSessions() {
		sessionData := &SessionsPageSession{
			Index:     index + 1,
			Key:       session.GetIpAddr(),
			FirstSeen: session.GetFirstSeen().Format("2006-01-02 15:04:05"),
			LastSeen:  session.GetLastSeen().Format("2006-01-02 15:04:05"),
			Requests:  session.GetRequests(),
			Tokens:    session.GetLimiterTokens(),
			Target:    "",
		}
		if lastClient := session.GetLastPoolClient(); lastClient != nil {
			sessionData.Target = lastClient.GetName()
		}

		validatorStats := session.GetValidatorStats()
		if validatorStats != nil {
			sessionData.ValidatorCount = validatorStats.Count
			sessionData.ValidatorStats = make([]SessionsPageSessionValidatorStats, len(validatorStats.Validators))
			for i, validator := range validatorStats.Validators {
				sessionData.ValidatorStats[i] = SessionsPageSessionValidatorStats{
					Start:  validator.Start,
					Length: validator.Length,
					Flag:   validator.Flag,
				}
			}
			sessionData.AggregatedRanges = fh.aggregateValidatorRanges(sessionData.ValidatorStats)
		}
		pageData.TotalRequests += sessionData.Requests
		pageData.TotalValidators += sessionData.ValidatorCount
		pageData.Sessions = append(pageData.Sessions, sessionData)
	}
	pageData.SessionCount = uint64(len(pageData.Sessions))

	return pageData, nil
}

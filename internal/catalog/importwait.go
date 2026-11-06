package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ImportStarted : travail d'import mis en file, avec son rapport s'il est déjà terminé.
type ImportStarted struct {
	JobID  string        `json:"job_id"`
	State  string        `json:"state"`
	Done   bool          `json:"done"`
	Report *ImportReport `json:"report,omitempty"`
	Error  string        `json:"error,omitempty"`
	Note   string        `json:"note"`
}

// StartImportWait met l'import en file puis attend éventuellement sa fin (WaitSecs).
func (s *Service) StartImportWait(ctx context.Context, in ImportInput) (*ImportStarted, error) {
	j, err := s.StartImport(ctx, in)
	if err != nil {
		return nil, err
	}
	out := &ImportStarted{JobID: j.ID, State: string(j.State),
		Note: "Import en file (non destructif : les originaux ne sont ni déplacés ni modifiés). Suivre avec get_job_status(job_id) ; l'analyse et les aperçus sont calculés ensuite en arrière-plan."}
	if in.WaitSecs <= 0 {
		return out, nil
	}
	if in.WaitSecs > 120 {
		in.WaitSecs = 120
	}
	state, done, err := s.WaitJob(ctx, j.ID, time.Duration(in.WaitSecs)*time.Second)
	if err != nil {
		return nil, err
	}
	out.State, out.Done = state, done
	if !done {
		return out, nil
	}
	fin, err := s.d.Queue.Get(ctx, j.ID)
	if err != nil {
		return nil, err
	}
	if fin.Error != nil {
		out.Error = fin.Error.Code + " : " + fin.Error.Message
	}
	var rep ImportReport
	if len(fin.Result) > 0 && json.Unmarshal(fin.Result, &rep) == nil {
		out.Report = &rep
		out.Note = fmt.Sprintf("Import terminé : %v. Aperçus et analyses en arrière-plan.", rep.Counts)
	}
	return out, nil
}

package billing

import (
	"context"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/service"
)

type Service struct {
	repo   *Repository
	parser *QueryParser
}

func New(repo *Repository, parser *QueryParser) *Service { return &Service{repo, parser} }
func (s *Service) Configured() bool                      { return s != nil && s.repo != nil }
func (s *Service) Run(ctx context.Context, m service.Meta, tool string, in Input) (Report, error) {
	p, e := Normalize(in)
	if e != nil {
		return Report{}, e
	}
	snapshot, e := s.repo.Snapshot(ctx, m.UserID, p)
	if e != nil {
		return Report{}, e
	}
	r, e := Analyze(m.UserID, tool, p, snapshot)
	if e != nil {
		return r, e
	}
	if e = ValidateReport(r); e != nil {
		return Report{}, e
	}
	e = s.repo.Save(ctx, m, &r)
	return r, e
}
func (s *Service) Get(ctx context.Context, user int64, id string) (Report, error) {
	return s.repo.Get(ctx, user, id)
}

type QueryResult struct {
	Status             string   `json:"status"`
	ExecuteID          string   `json:"execute_id"`
	MissingInformation []string `json:"missing_information"`
	Report             *Report  `json:"report"`
}

func (s *Service) Query(ctx context.Context, m service.Meta, in QueryInput) (QueryResult, error) {
	out, e := s.parser.Parse(coze.WithIdentity(ctx, m.UserID, "billing-query:"+uuid.NewString()), in, m.TraceID)
	if e != nil {
		return QueryResult{}, e
	}
	result := QueryResult{Status: "awaiting_clarification", ExecuteID: out.ExecuteID, MissingInformation: out.Route.MissingInformation}
	if len(result.MissingInformation) > 0 {
		return result, nil
	}
	r, e := s.Run(ctx, m, out.Route.Tool, Input{From: out.Route.From, To: out.Route.To, Timezone: out.Route.Timezone, AccountID: in.AccountID})
	if e != nil {
		return result, e
	}
	result.Status = "analyzed"
	result.Report = &r
	return result, nil
}

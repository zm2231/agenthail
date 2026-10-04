package surface

import "time"

const (
	GoalStatusActive        = "active"
	GoalStatusPaused        = "paused"
	GoalStatusBlocked       = "blocked"
	GoalStatusUsageLimited  = "usageLimited"
	GoalStatusBudgetLimited = "budgetLimited"
	GoalStatusComplete      = "complete"
)

type GoalState struct {
	Objective       string    `json:"objective"`
	Status          string    `json:"status"`
	TimeUsedSeconds int64     `json:"timeUsedSeconds"`
	TokensUsed      int64     `json:"tokensUsed"`
	TokenBudget     *int64    `json:"tokenBudget"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

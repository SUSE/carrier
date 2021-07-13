package models

import (
	"fmt"
	"strings"
)

// This subsection of models provides structures related to the
// environment variables of applications.

// Show Response
type EnvVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Set Request, List Response
type EnvVariableList []EnvVariable

// Unset Request, Match Response
type EnvVarnameList []string

// Implement the Sort interface for EV definition slices

func (ev EnvVariableList) Len() int {
	return len(ev)
}

func (ev EnvVariableList) Swap(i, j int) {
	ev[i], ev[j] = ev[j], ev[i]
}

func (ev EnvVariableList) Less(i, j int) bool {
	return ev[i].Name < ev[j].Name
}

func (ev EnvVariableList) ToString(name string) string {
	assignments := []string{
		fmt.Sprintf(`{ "name": "%s", "value": "%s"}`, `PORT`, `8080`),
	}
	for _, ev := range ev {
		assignments = append(assignments,
			fmt.Sprintf(`{ "name": "%s", "valueFrom": { "secretKeyRef": {"key":"%s","name":"%s"}}}`,
				ev.Name, ev.Name, name+"-env"))
	}
	return `[` + strings.Join(assignments, ",") + `]`
}

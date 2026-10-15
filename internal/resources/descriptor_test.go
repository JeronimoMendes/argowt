package resources

import "testing"

func TestLookupAliases(t *testing.T) {
	for alias, want := range map[string]Kind{
		"wf":       Workflows,
		"wft":      WorkflowTemplates,
		"cwft":     ClusterWorkflowTemplates,
		"cron":     CronWorkflows,
		"es":       EventSources,
		"sensor":   Sensors,
		"eventbus": EventBuses,
	} {
		descriptor, found := Lookup(alias)
		if !found || descriptor.Kind != want {
			t.Fatalf("Lookup(%q) = %+v, %t", alias, descriptor, found)
		}
	}
}

func TestCommandsRespectAvailabilityAndFuzzySearch(t *testing.T) {
	available := map[Kind]bool{Workflows: true, WorkflowTemplates: true, EventSources: false}
	commands := Commands(available, "wft")
	if len(commands) != 1 || commands[0].Kind != WorkflowTemplates {
		t.Fatalf("Commands() = %+v", commands)
	}
	for _, command := range Commands(available, "") {
		if command.Kind == EventSources {
			t.Fatal("unavailable command was returned")
		}
	}
}

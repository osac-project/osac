package subscription

import (
	"strings"
	"testing"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func TestCommandRegistersSubscriptionOperations(t *testing.T) {
	command := Cmd()
	for _, name := range []string{"list", "get", "create", "update", "delete"} {
		if _, _, err := command.Find([]string{name}); err != nil {
			t.Errorf("Find(%q): %v", name, err)
		}
	}
}

func TestRenderList(t *testing.T) {
	item := &publicv1.Subscription{
		Id: "tenant-a-access",
		Spec: &publicv1.SubscriptionSpec{
			Organization: &publicv1.SubscriptionOrganization{Id: "tenant-a"},
			Priority:     7,
			ModelRefs:    []*publicv1.SubscriptionModelRef{{Name: "granite-3b"}, {Name: "llama-8b"}},
		},
		Status: &publicv1.SubscriptionStatus{Phase: "Active"},
	}
	var output strings.Builder
	if err := renderList(&output, []*publicv1.Subscription{item}); err != nil {
		t.Fatalf("renderList: %v", err)
	}
	normalized := strings.Join(strings.Fields(output.String()), " ")
	for _, want := range []string{"ID TENANT PRIORITY STATUS MODELS", "tenant-a-access tenant-a 7 Active granite-3b,llama-8b"} {
		if !strings.Contains(normalized, want) {
			t.Errorf("renderList output %q does not contain %q", normalized, want)
		}
	}
}

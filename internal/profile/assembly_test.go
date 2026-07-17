package profile

import (
	"errors"
	"reflect"
	"testing"
)

func TestPlanNewProject_Single(t *testing.T) {
	s := ScaffoldDef{Name: "library-go", Layout: LayoutSingle}
	vars := map[string]string{"module_path": "github.com/wirvii/x"}

	got, err := PlanNewProject(s, ProjectChoices{Dest: "/tmp/newrepo", Vars: vars})
	if err != nil {
		t.Fatalf("PlanNewProject: %v", err)
	}

	want := AssemblyPlan{
		Bootstrap: nil,
		Copies: []CopyStep{{
			Src:  "scaffolds/library-go/skeleton",
			Dest: "/tmp/newrepo",
			Vars: vars,
		}},
		GitInit: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plan = %+v, want %+v", got, want)
	}
}

func TestPlanNewProject_Deterministic(t *testing.T) {
	s := ScaffoldDef{Name: "lib", Layout: LayoutSingle}
	choices := ProjectChoices{Dest: "/tmp/x", Vars: map[string]string{"a": "b"}}

	p1, err1 := PlanNewProject(s, choices)
	p2, err2 := PlanNewProject(s, choices)
	if err1 != nil || err2 != nil {
		t.Fatalf("errs: %v %v", err1, err2)
	}
	if !reflect.DeepEqual(p1, p2) {
		t.Errorf("plan not deterministic:\n%+v\n%+v", p1, p2)
	}
}

func TestPlanNewProject_MonorepoDeferred(t *testing.T) {
	s := ScaffoldDef{Name: "saas", Layout: LayoutMonorepo, Toolchain: ToolchainTurborepo, Bootstrap: "create-turbo@2.3.1"}
	_, err := PlanNewProject(s, ProjectChoices{Dest: "/tmp/x"})
	if !errors.Is(err, ErrLayoutUnsupported) {
		t.Fatalf("want ErrLayoutUnsupported for monorepo in §7a, got %v", err)
	}
}

func TestPlanNewProject_MissingDest(t *testing.T) {
	s := ScaffoldDef{Name: "lib", Layout: LayoutSingle}
	_, err := PlanNewProject(s, ProjectChoices{})
	if err == nil {
		t.Fatal("want error for empty dest")
	}
}

func TestPlanNewProject_BadLayout(t *testing.T) {
	s := ScaffoldDef{Name: "x", Layout: Layout("weird")}
	_, err := PlanNewProject(s, ProjectChoices{Dest: "/tmp/x"})
	if !errors.Is(err, ErrInvalidLayout) {
		t.Fatalf("want ErrInvalidLayout, got %v", err)
	}
}

package build

import (
	"context"
	"reflect"
	"testing"
)

type fakeStep struct {
	name string
	deps []string
}

func (f fakeStep) Name() string                            { return f.name }
func (f fakeStep) DependsOn() []string                     { return f.deps }
func (f fakeStep) Run(context.Context, func(Event)) error  { return nil }

func TestTopologicalSort(t *testing.T) {
	a := fakeStep{name: "a"}
	b := fakeStep{name: "b", deps: []string{"a"}}
	c := fakeStep{name: "c", deps: []string{"b"}}
	d := fakeStep{name: "d", deps: []string{"a"}}
	got, err := TopoSort([]Step{c, d, b, a})
	if err != nil {
		t.Fatalf("TopoSort error: %v", err)
	}
	names := make([]string, len(got))
	for i, s := range got {
		names[i] = s.Name()
	}
	idx := func(n string) int {
		for i, s := range names {
			if s == n {
				return i
			}
		}
		return -1
	}
	if !(idx("a") < idx("b") && idx("b") < idx("c") && idx("a") < idx("d")) {
		t.Errorf("invalid topological order: %v", names)
	}
}

func TestTopoSortCycle(t *testing.T) {
	a := fakeStep{name: "a", deps: []string{"b"}}
	b := fakeStep{name: "b", deps: []string{"a"}}
	_, err := TopoSort([]Step{a, b})
	if err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestTopoSortMissingDep(t *testing.T) {
	a := fakeStep{name: "a", deps: []string{"ghost"}}
	_, err := TopoSort([]Step{a})
	if err == nil {
		t.Fatal("expected unknown-dependency error")
	}
}

func TestTopoSortStable(t *testing.T) {
	a := fakeStep{name: "a"}
	b := fakeStep{name: "b"}
	c := fakeStep{name: "c"}
	got1, _ := TopoSort([]Step{a, b, c})
	got2, _ := TopoSort([]Step{a, b, c})
	if !reflect.DeepEqual(stepNames(got1), stepNames(got2)) {
		t.Errorf("topo sort is unstable: %v vs %v", stepNames(got1), stepNames(got2))
	}
}

func stepNames(ss []Step) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Name()
	}
	return out
}

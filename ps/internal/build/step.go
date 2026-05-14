package build

import (
	"context"
	"fmt"
)

// Step is the unit of work in a build plan. Run must respect ctx
// cancellation and call emit for every observable state change.
// Steps must not write to shared state outside what they emit.
type Step interface {
	Name() string
	DependsOn() []string
	Run(ctx context.Context, emit func(Event)) error
}

// TopoSort returns steps in dependency order, preserving input order for
// steps whose dependencies are equally satisfied. Returns an error on
// cycles or unknown dependencies.
func TopoSort(steps []Step) ([]Step, error) {
	byName := make(map[string]Step, len(steps))
	for _, s := range steps {
		if _, dup := byName[s.Name()]; dup {
			return nil, fmt.Errorf("duplicate step name: %s", s.Name())
		}
		byName[s.Name()] = s
	}
	for _, s := range steps {
		for _, d := range s.DependsOn() {
			if _, ok := byName[d]; !ok {
				return nil, fmt.Errorf("step %s depends on unknown step %s", s.Name(), d)
			}
		}
	}
	state := make(map[string]int, len(steps)) // 0 unvisited, 1 in-stack, 2 done
	out := make([]Step, 0, len(steps))
	var visit func(s Step) error
	visit = func(s Step) error {
		switch state[s.Name()] {
		case 1:
			return fmt.Errorf("cycle through step %s", s.Name())
		case 2:
			return nil
		}
		state[s.Name()] = 1
		for _, d := range s.DependsOn() {
			if err := visit(byName[d]); err != nil {
				return err
			}
		}
		state[s.Name()] = 2
		out = append(out, s)
		return nil
	}
	for _, s := range steps {
		if err := visit(s); err != nil {
			return nil, err
		}
	}
	return out, nil
}

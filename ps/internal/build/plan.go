package build

// Plan is a topologically sorted list of Steps to execute. Construction
// from Selections + Config happens in BuildPlan (added in a later task).
type Plan struct {
	Steps []Step
}

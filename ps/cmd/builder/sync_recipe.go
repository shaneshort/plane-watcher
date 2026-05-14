package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/plane-watcher/plane-feeder/internal/build/config"
	"github.com/plane-watcher/plane-feeder/internal/build/recipe"
)

// runSyncRecipe handles `builder sync-recipe [--write]`. Without
// --write, exits with code 1 if the .inc would change. With --write,
// rewrites the .inc.
func runSyncRecipe(repoRoot string, args []string, stdout io.Writer) int {
	fs := flag.NewFlagSet("sync-recipe", flag.ContinueOnError)
	write := fs.Bool("write", false, "rewrite the .inc; without this flag the command is read-only")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stdout, err)
		return 2
	}
	cfg, err := config.LoadAll(config.LoadAllOptions{RepoRoot: repoRoot})
	if err != nil {
		fmt.Fprintln(stdout, "config load:", err)
		return 1
	}
	want := recipe.RenderInc(cfg.Commands)
	got, err := os.ReadFile(cfg.RecipeIncludePath)
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(stdout, "read .inc:", err)
		return 1
	}
	if bytes.Equal(got, []byte(want)) {
		fmt.Fprintln(stdout, "up-to-date:", cfg.RecipeIncludePath)
		return 0
	}
	if !*write {
		fmt.Fprintln(stdout, "out of sync:", cfg.RecipeIncludePath)
		fmt.Fprintln(stdout, "run: builder sync-recipe --write")
		return 1
	}
	if err := recipe.Write(cfg.RecipeIncludePath, cfg.Commands); err != nil {
		fmt.Fprintln(stdout, "write .inc:", err)
		return 1
	}
	fmt.Fprintln(stdout, "wrote:", cfg.RecipeIncludePath)
	return 0
}

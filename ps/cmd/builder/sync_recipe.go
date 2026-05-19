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

// runSyncRecipe handles `builder sync-recipe [--write]`.
//
// Without --write: read-only drift check. Exits 1 if the generated
// plane-watcher-tools-binaries.inc is out of sync with what
// ps/cmd/build.toml says, 0 if it matches.
//
// With --write: regenerates the .inc.
//
// As of Phase D4 the recipe lives in firmware/yocto/meta-plane-watcher/;
// the .bb / files/ scaffolding helpers that used to also live here are
// gone, since the recipe is now hand-maintained (systemd unit + image
// recipe integration, none of which the generator could produce
// faithfully).
func runSyncRecipe(repoRoot string, args []string, stdout io.Writer) int {
	fs := flag.NewFlagSet("sync-recipe", flag.ContinueOnError)
	write := fs.Bool("write", false, "apply changes; without this flag the command is read-only")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stdout, err)
		return 2
	}
	cfg, err := config.LoadAll(config.LoadAllOptions{RepoRoot: repoRoot})
	if err != nil {
		fmt.Fprintln(stdout, "config load:", err)
		return 1
	}

	wantInc := recipe.RenderInc(cfg.Commands)
	gotInc, err := os.ReadFile(cfg.RecipeIncludePath)
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(stdout, "read .inc:", err)
		return 1
	}
	if bytes.Equal(gotInc, []byte(wantInc)) {
		fmt.Fprintln(stdout, "up-to-date:", cfg.RecipeIncludePath)
		return 0
	}

	if !*write {
		fmt.Fprintln(stdout, "out of sync:", cfg.RecipeIncludePath, "— regenerated content differs")
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

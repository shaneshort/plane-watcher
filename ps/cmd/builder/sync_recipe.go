package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/plane-watcher/plane-feeder/internal/build/config"
	"github.com/plane-watcher/plane-feeder/internal/build/recipe"
)

// runSyncRecipe handles `builder sync-recipe [--write]`.
//
// Without --write: read-only drift check. Exits 1 if anything is out
// of sync (the .inc, the defaults-file copy in the recipe vs the
// source-of-truth in ps/cmd/plane-feeder/), 0 if everything matches.
//
// With --write: scaffolds the .bb + init script on first use,
// regenerates the .inc, and copies the defaults file from
// ps/cmd/plane-feeder/ into the recipe's files/ dir.
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
	recipeDir := filepath.Dir(cfg.RecipeIncludePath)

	// --- Drift accumulator. Each target is independent. ---
	driftCount := 0
	check := func(label string, ok bool, detail string) {
		if !ok {
			driftCount++
			fmt.Fprintln(stdout, "out of sync:", label, "—", detail)
		}
	}

	// 1. .inc drift
	wantInc := recipe.RenderInc(cfg.Commands)
	gotInc, err := os.ReadFile(cfg.RecipeIncludePath)
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(stdout, "read .inc:", err)
		return 1
	}
	incInSync := bytes.Equal(gotInc, []byte(wantInc))
	check(cfg.RecipeIncludePath, incInSync, "regenerated content differs")

	// 2. defaults-file drift (only meaningful if the recipeDir/files/ exists,
	//    i.e. the .bb has been scaffolded at least once).
	defaultsErr := recipe.VerifyDefaultsFile(repoRoot, recipeDir)
	defaultsInSync := defaultsErr == nil
	if !defaultsInSync {
		var d *recipe.DefaultsDriftError
		if errors.As(defaultsErr, &d) {
			check(recipe.DefaultsFileRecipePath(recipeDir), false, d.Reason)
		} else {
			// Source file missing → real error, not drift.
			fmt.Fprintln(stdout, "defaults check:", defaultsErr)
			return 1
		}
	}

	if driftCount == 0 {
		fmt.Fprintln(stdout, "up-to-date:", cfg.RecipeIncludePath)
		fmt.Fprintln(stdout, "up-to-date:", recipe.DefaultsFileRecipePath(recipeDir))
		return 0
	}

	if !*write {
		fmt.Fprintln(stdout, "run: builder sync-recipe --write")
		return 1
	}

	// --- Apply changes. ---

	// Scaffold .bb (one-time) before any file writes so files/ exists.
	bbPath := filepath.Join(recipeDir, "plane-watcher-tools.bb")
	createdBB, err := recipe.EnsureBB(bbPath)
	if err != nil {
		fmt.Fprintln(stdout, "scaffold .bb:", err)
		return 1
	}
	if createdBB {
		fmt.Fprintln(stdout, "scaffolded:", bbPath)
	}
	createdInit, err := recipe.EnsureInitScript(recipeDir)
	if err != nil {
		fmt.Fprintln(stdout, "scaffold init script:", err)
		return 1
	}
	if createdInit {
		fmt.Fprintln(stdout, "scaffolded:", filepath.Join(recipeDir, "files", "plane-feeder.init"))
	}

	if !incInSync {
		if err := recipe.Write(cfg.RecipeIncludePath, cfg.Commands); err != nil {
			fmt.Fprintln(stdout, "write .inc:", err)
			return 1
		}
		fmt.Fprintln(stdout, "wrote:", cfg.RecipeIncludePath)
	}

	if !defaultsInSync {
		if err := recipe.SyncDefaultsFile(repoRoot, recipeDir); err != nil {
			fmt.Fprintln(stdout, "sync defaults file:", err)
			return 1
		}
		fmt.Fprintln(stdout, "synced:", recipe.DefaultsFileRecipePath(recipeDir))
	}

	if createdBB {
		fmt.Fprintln(stdout, "next: add `plane-watcher-tools` to IMAGE_INSTALL in petalinux-image-minimal.bbappend")
	}
	return 0
}

// Go script for calculating the build diff between branches.
// This script expects to run within a gitlab ci pod.
package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
)

// Helper function to fetch an upstream target branch
func FetchBranch(target_branch string) {

	fmt.Println("Fetching: " + target_branch)

	cmd := exec.Command("git", "fetch", "origin", target_branch)
	if err := cmd.Run(); err != nil {
		log.Fatal(err)
	}
}

// Helper function to calculate diffs between two refs
func CalculateDiff(target_branch string, current_branch string, outfile *os.File) {

	fmt.Println("Calculating diffs between:" + current_branch + " and: " + target_branch)

	cmd := exec.Command("git", "diff", "--name-only", current_branch, "origin/"+target_branch)

	// Set output to git-diff file created earlier
	cmd.Stdout = outfile
	fmt.Println(cmd.String())

	if err := cmd.Run(); err != nil {
		log.Fatal(err)
	}
}

func main() {

	CI_COMMIT_BRANCH, ok := os.LookupEnv("CI_COMMIT_BRANCH")
	if !ok {
		panic("CI_COMMIT_BRANCH env not set")
	}

	// Create git diff file. This file is in .gitignore so it won't be commited.
	outfile, err := os.Create("git-diff")
	if err != nil {
		panic("Failed to create git diff file")
		log.Fatal(err)
	}

	// If current branch is master, then all dashboards are in the diff.
	if CI_COMMIT_BRANCH == "master" {

		// Git ls-files to list all files in the repo (As this is master)
		cmd := exec.Command("git", "ls-files")
		cmd.Stdout = outfile

		// Run the command
		if err := cmd.Run(); err != nil {
			log.Fatal(err)
		}

	} else {

		// For all other branches we compare the current branch to commit_before_sha.
		// This is essentially comparing to the previous latest commit present on a branch.
		// Refer: https://docs.gitlab.com/ee/ci/variables/predefined_variables.html
		COMMIT_BEFORE_SHA, ok := os.LookupEnv("COMMIT_BEFORE_SHA")
		if !ok {
			panic("COMMIT_BEFORE_SHA env not set")
		}

		// Fetch information about the current branch
		FetchBranch(CI_COMMIT_BRANCH)

		// Calculate diff and save to outfile
		CalculateDiff(CI_COMMIT_BRANCH, COMMIT_BEFORE_SHA, outfile)
	}
}

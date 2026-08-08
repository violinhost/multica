package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var managedActionCmd = &cobra.Command{
	Use:   "managed-action",
	Short: "Discover and invoke native managed actions",
}

var managedActionDiscoverCmd = &cobra.Command{
	Use:   "discover <project-id>",
	Short: "List managed actions enabled for a project",
	Args:  exactArgs(1),
	RunE:  runManagedActionDiscover,
}

var managedActionStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a native managed action from a versioned request JSON file",
	RunE:  runManagedActionStart,
}

func init() {
	managedActionCmd.AddCommand(managedActionDiscoverCmd, managedActionStartCmd)
	managedActionDiscoverCmd.Flags().String("output", "json", "Output format: json or table")
	managedActionStartCmd.Flags().String("request-file", "", "Path to the versioned managed-action request JSON (required)")
	managedActionStartCmd.Flags().String("output", "json", "Output format: json")
}

func runManagedActionDiscover(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	project, err := resolveProjectID(ctx, client, args[0])
	if err != nil {
		return fmt.Errorf("resolve project: %w", err)
	}
	var response map[string]any
	if err := client.GetJSON(ctx, "/api/managed-actions/projects/"+project.ID, &response); err != nil {
		return fmt.Errorf("discover managed actions: %w", err)
	}
	return cli.PrintJSON(os.Stdout, response)
}

func runManagedActionStart(cmd *cobra.Command, _ []string) error {
	path, _ := cmd.Flags().GetString("request-file")
	if path == "" {
		return fmt.Errorf("--request-file is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read managed action request: %w", err)
	}
	var request map[string]any
	if err := json.Unmarshal(data, &request); err != nil {
		return fmt.Errorf("parse managed action request: %w", err)
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var receipt map[string]any
	if err := client.PostJSON(ctx, "/api/managed-actions/start", request, &receipt); err != nil {
		return fmt.Errorf("start managed action: %w", err)
	}
	return cli.PrintJSON(os.Stdout, receipt)
}

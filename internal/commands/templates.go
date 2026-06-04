package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

// instanceTemplate is a launch preset for `instances create --template <id>`.
// Mirrors the canonical list in the app (src/lib/templates.ts); these IDs are
// what the provisioner's launch-script generator understands.
type instanceTemplate struct {
	ID          string
	Name        string
	GPU         string
	Description string
}

var instanceTemplates = []instanceTemplate{
	{"ubuntu-devbox", "Devbox", "optional", "Clean dev environment: Python + uv, VS Code Server, Jupyter, Docker."},
	{"ubuntu-train", "Finetune", "required", "Fine-tuning: Unsloth, PyTorch + CUDA, HuggingFace CLI, W&B."},
	{"ubuntu-inference", "Inference", "required", "Model serving: vLLM or SGLang, HuggingFace CLI, GPU monitoring."},
}

var templatesCmd = &cobra.Command{
	Use:   "templates",
	Short: "List instance templates",
	Long:  "List the instance templates available for `runcrate instances create --template <id>`.",
	RunE: func(cmd *cobra.Command, args []string) error {
		w := newTable()
		fmt.Fprintf(w, "ID\tNAME\tGPU\tDESCRIPTION\n")
		for _, t := range instanceTemplates {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.ID, t.Name, t.GPU, t.Description)
		}
		if err := w.Flush(); err != nil {
			return err
		}
		fmt.Println()
		fmt.Println("Launch with:  runcrate instances create --name <name> --gpu <type> --template <id>")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(templatesCmd)
}

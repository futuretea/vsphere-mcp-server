package cmd_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/futuretea/vsphere-mcp-server/internal/cmd"
)

func TestRootHelpListsSubcommands(t *testing.T) {
	var out bytes.Buffer
	root := cmd.NewRootCommand(cmd.IOStreams{Out: &out, ErrOut: &out})
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("help: %v", err)
	}
	text := out.String()
	for _, want := range []string{"mcp", "tools", "version", "completion"} {
		if !strings.Contains(text, want) {
			t.Fatalf("help missing %q:\n%s", want, text)
		}
	}
}

func TestRootWithoutArgsShowsHelp(t *testing.T) {
	var out bytes.Buffer
	root := cmd.NewRootCommand(cmd.IOStreams{Out: &out, ErrOut: &out})
	root.SetArgs([]string{})
	if err := root.Execute(); err != nil {
		t.Fatalf("root with no args: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "Available Commands") {
		t.Fatalf("expected help output, got:\n%s", text)
	}
	if strings.Contains(text, "registered MCP tools") || strings.Contains(text, "starting HTTP MCP server") {
		t.Fatalf("root must not start the MCP server:\n%s", text)
	}
}

func TestToolsListAndCall(t *testing.T) {
	var out bytes.Buffer
	root := cmd.NewRootCommand(cmd.IOStreams{Out: &out, ErrOut: &out})
	root.SetArgs([]string{"tools", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("tools list: %v", err)
	}
	if !strings.Contains(out.String(), "vsphere_list_inventory") || !strings.Contains(out.String(), "vsphere_query_metrics") {
		t.Fatalf("tools list missing vSphere queries:\n%s", out.String())
	}

	out.Reset()
	root = cmd.NewRootCommand(cmd.IOStreams{Out: &out, ErrOut: &out})
	root.SetArgs([]string{"tools", "call", "vsphere_list_inventory", "--params", `{"kind":"vm"}`})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unconfigured tools call error = %v", err)
	}
}

func TestVersionCommand(t *testing.T) {
	var out bytes.Buffer
	root := cmd.NewRootCommand(cmd.IOStreams{Out: &out, ErrOut: &out})
	root.SetArgs([]string{"version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.Contains(out.String(), "vsphere-mcp-server") {
		t.Fatalf("unexpected version output: %q", out.String())
	}
}

func TestToolsUnknownTool(t *testing.T) {
	var out bytes.Buffer
	root := cmd.NewRootCommand(cmd.IOStreams{Out: &out, ErrOut: &out})
	root.SetArgs([]string{"tools", "describe", "missing-tool"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("expected unknown tool error, got %v", err)
	}

	root = cmd.NewRootCommand(cmd.IOStreams{Out: &out, ErrOut: &out})
	root.SetArgs([]string{"tools", "call", "missing-tool", "--params", "{}"})
	err = root.Execute()
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("expected unknown tool error on call, got %v", err)
	}
}

func TestToolsRejectsNullParams(t *testing.T) {
	var out bytes.Buffer
	root := cmd.NewRootCommand(cmd.IOStreams{Out: &out, ErrOut: &out})
	root.SetArgs([]string{"tools", "call", "vsphere_list_tasks", "--params", "null"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "expected an object") {
		t.Fatalf("expected JSON object error, got %v", err)
	}
}

func TestCommandsReturnOutputErrors(t *testing.T) {
	writeErr := errors.New("write failed")
	tests := []struct {
		name string
		args []string
	}{
		{name: "completion script", args: []string{"completion", "bash"}},
		{name: "completion install", args: []string{"completion", "install"}},
		{name: "version", args: []string{"version"}},
		{name: "tools list", args: []string{"tools", "list"}},
		{name: "tools describe", args: []string{"tools", "describe", "vsphere_list_inventory"}},
		{name: "tools describe duplicate", args: []string{"tools", "describe", "vsphere_list_inventory"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := cmd.NewRootCommand(cmd.IOStreams{
				In:     strings.NewReader(""),
				Out:    errorWriter{err: writeErr},
				ErrOut: io.Discard,
			})
			root.SetArgs(tt.args)
			if err := root.Execute(); !errors.Is(err, writeErr) {
				t.Fatalf("Execute() error = %v, want %v", err, writeErr)
			}
		})
	}
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

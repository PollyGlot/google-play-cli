package kernel

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func RejectRepeatedFlags(cmd *cobra.Command) *cobra.Command {
	if cmd == nil {
		return cmd
	}
	renderUnwrapped(cmd)
	wrapSubtree(cmd)
	return cmd
}

func wrapSubtree(cmd *cobra.Command) {
	cmd.PersistentFlags().VisitAll(wrapOnce)
	cmd.Flags().VisitAll(wrapOnce)
	for _, sub := range cmd.Commands() {
		wrapSubtree(sub)
	}
}

func wrapOnce(f *pflag.Flag) {
	if f == nil || f.Value == nil {
		return
	}
	if _, already := f.Value.(*onceValue); already {
		return
	}
	if repeatable(f.Value) {
		return
	}
	f.Value = &onceValue{Value: f.Value, name: f.Name}
}

var mapFlagTypes = map[string]bool{
	"stringToString": true,
	"stringToInt":    true,
	"stringToInt64":  true,
}

func repeatable(v pflag.Value) bool {
	if _, slice := v.(pflag.SliceValue); slice {
		return true
	}
	return mapFlagTypes[v.Type()]
}

func renderUnwrapped(cmd *cobra.Command) {
	help, usage := cmd.HelpFunc(), cmd.UsageFunc()
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		defer unwrapSubtree(c.Root())()
		help(c, args)
	})
	cmd.SetUsageFunc(func(c *cobra.Command) error {
		defer unwrapSubtree(c.Root())()
		return usage(c)
	})
}

func AllowRepeatedFlags(cmd *cobra.Command) {
	unwrapSubtree(cmd)
}

func unwrapSubtree(cmd *cobra.Command) func() {
	var undo []func()
	visit := func(f *pflag.Flag) {
		wrapper, wrapped := f.Value.(*onceValue)
		if !wrapped {
			return
		}
		f.Value = wrapper.Value
		undo = append(undo, func() { f.Value = wrapper })
	}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		c.PersistentFlags().VisitAll(visit)
		c.Flags().VisitAll(visit)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(cmd)
	return func() {
		for _, restore := range undo {
			restore()
		}
	}
}

type onceValue struct {
	pflag.Value
	name  string
	first string
	set   bool
}

func (v *onceValue) Set(s string) error {
	if v.set {
		return fmt.Errorf("repeated flag: --%s was already set to %q; pass it once", v.name, v.first)
	}
	if err := v.Value.Set(s); err != nil {
		return err
	}
	v.set = true
	v.first = s
	return nil
}

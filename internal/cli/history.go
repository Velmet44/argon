package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/Velmet44/argon/internal/config"
)

func newHistoryCmd() *cobra.Command {
	c := &cobra.Command{Use: "history", Short: "View/clear local log (never sent unless flagged)"}
	c.AddCommand(&cobra.Command{
		Use:   "clear",
		Short: "Wipe ~/.argon/history.jsonl",
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := config.Dir()
			if err != nil {
				return err
			}
			p := filepath.Join(d, "history.jsonl")
			fmt.Print("clear argon history log? [y/N] ")
			r, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			if r == "" || (r[0] != 'y' && r[0] != 'Y') {
				return nil
			}
			return os.Remove(p)
		},
	})
	c.RunE = func(cmd *cobra.Command, args []string) error {
		d, err := config.Dir()
		if err != nil {
			return err
		}
		f, err := os.Open(filepath.Join(d, "history.jsonl"))
		if err != nil {
			fmt.Println("(empty)")
			return nil
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		n := 0
		for sc.Scan() && n < 20 {
			fmt.Println(sc.Text())
			n++
		}
		return nil
	}
	return c
}

// storage-migrate is an operator-only offline migration command. It is not
// included in the HTTP or Agent tool surface.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/platform"
	"github.com/qingyingjin/banking-service/internal/storage"
)

func openEnv(path string) (*platform.Connections, string, error) {
	values, e := godotenv.Read(path)
	if e != nil {
		return nil, "", fmt.Errorf("cannot read configuration file")
	}
	cfg, e := config.Load(func(key string) string { return values[key] })
	if e != nil {
		return nil, "", e
	}
	conn, e := platform.Open(cfg)
	if e != nil {
		return nil, "", fmt.Errorf("cannot open configured database")
	}
	return conn, cfg.DatabaseDriver, nil
}

func run() int {
	sourcePath := flag.String("source-env", "", "source Banking configuration file (secrets stay in memory)")
	targetPath := flag.String("target-env", "", "target Banking configuration file (secrets stay in memory)")
	mode := flag.String("mode", "verify", "import or verify; control operations use storage-control")
	allowBootstrap := flag.Bool("allow-bootstrap-rules", false, "allow replacement only of the exact six pristine baseline risk rules")
	timeout := flag.Duration("timeout", 10*time.Minute, "overall migration deadline, maximum one hour")
	output := flag.String("output", "", "optional aggregate JSON report path; contains no financial row contents")
	flag.Parse()
	if *sourcePath == "" || *targetPath == "" || *sourcePath == *targetPath || (*mode != "verify" && *mode != "import") || *timeout <= 0 || *timeout > time.Hour {
		fmt.Fprintln(os.Stderr, "distinct source-env/target-env, mode import|verify and bounded positive timeout required")
		return 2
	}
	source, sourceDialect, e := openEnv(*sourcePath)
	if e != nil {
		fmt.Fprintln(os.Stderr, "source configuration/database unavailable")
		return 1
	}
	defer source.Close()
	target, targetDialect, e := openEnv(*targetPath)
	if e != nil {
		fmt.Fprintln(os.Stderr, "target configuration/database unavailable")
		return 1
	}
	defer target.Close()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	var report storage.Report
	if *mode == "import" {
		report, e = storage.Import(ctx, source.SQL, target.SQL, sourceDialect, targetDialect, storage.ImportOptions{AllowBootstrapRules: *allowBootstrap})
	} else {
		report, e = storage.Compare(ctx, source.SQL, target.SQL, sourceDialect, targetDialect)
	}
	raw, marshalErr := json.MarshalIndent(report, "", "  ")
	if marshalErr != nil {
		fmt.Fprintln(os.Stderr, "cannot encode aggregate report")
		return 1
	}
	fmt.Println(string(raw))
	if *output != "" {
		if writeErr := os.WriteFile(*output, append(raw, '\n'), 0600); writeErr != nil {
			fmt.Fprintln(os.Stderr, "database result is shown above; cannot write aggregate report file; a successful import remains committed")
			return 1
		}
	}
	if e != nil {
		// SQL driver errors can contain duplicate key text or row details. Keep
		// CLI logs safe; aggregate table hashes/check names show the failed gate.
		fmt.Fprintln(os.Stderr, "storage operation refused or failed; databases remain fenced; inspect aggregate report and operator configuration")
		return 1
	}
	return 0
}

func main() { os.Exit(run()) }

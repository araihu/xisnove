// Frozen compatibility probe for the Xisnove runtime immediately preceding
// schema 11. Keep this source standalone: it must not import Xisnove packages.
package main

import (
	"flag"
	"fmt"
	"os"
)

const (
	minimumSchemaVersion int64 = 10
	maximumSchemaVersion int64 = 11
)

func main() {
	schemaVersion := flag.Int64("schema-version", 0, "applied schema version")
	flag.Parse()
	if *schemaVersion <= 0 || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: xisnove-n-minus-one-probe --schema-version VERSION")
		os.Exit(2)
	}
	if *schemaVersion < minimumSchemaVersion || *schemaVersion > maximumSchemaVersion {
		fmt.Fprintf(os.Stderr, "not ready schema=%d interval=[%d,%d]\n", *schemaVersion, minimumSchemaVersion, maximumSchemaVersion)
		os.Exit(1)
	}
	fmt.Printf("ready schema=%d interval=[%d,%d]\n", *schemaVersion, minimumSchemaVersion, maximumSchemaVersion)
}

//go:build ignore

// gen rebuilds internal/catalog/projects.json, the snapshot embedded in the
// program: BOINC's official project list plus a few well-known projects that
// are not on it. Community projects are only kept if their server answers
// get_project_config.php right now.
//
// Run from the repository root: go run internal/catalog/gen/main.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/alplix/iris/internal/catalog"
)

var community = []catalog.Project{
	{Name: "Constellation", URL: "https://boinc.multi-pool.info/latinsquares/", Area: "Mathematics, computing, and games", SubArea: "Mathematics",
		Description: "Searches for Latin squares and related combinatorial objects."},
	{Name: "TN-Grid", URL: "https://gene.disi.unitn.it/test/", Area: "Biology and Medicine", SubArea: "Genomics",
		Description: "Gene network inference for biomedical research (University of Trento)."},
	{Name: "Collatz Conjecture", URL: "https://boinc.thesonntags.com/collatz/", Area: "Mathematics, computing, and games", SubArea: "Mathematics",
		Description: "Tests the Collatz (3n+1) conjecture for very large numbers."},
	{Name: "Enigma@Home", URL: "http://www.enigmaathome.net/", Area: "Mathematics, computing, and games", SubArea: "Cryptography",
		Description: "Breaks original German Enigma messages from World War II."},
	{Name: "DrugDiscovery@Home", URL: "https://boinc.drugdiscoveryathome.com/", Area: "Biology and Medicine", SubArea: "Drug discovery",
		Description: "Molecular simulations to help find new drugs."},
	{Name: "WUProp@Home", URL: "https://wuprop.boinc-af.org/", Area: "Mathematics, computing, and games", SubArea: "Computing",
		Description: "Measures the properties of BOINC work units."},
	{Name: "Citizen Science Grid", URL: "https://csgrid.org/csg/", Area: "Multiple applications", SubArea: "Multiple",
		Description: "Astrophysics, biology and other science from Rensselaer Polytechnic Institute."},
	{Name: "Radioactive@Home", URL: "http://radioactiveathome.org/boinc/", Area: "Physical Science", SubArea: "Radiation",
		Description: "Volunteer radiation monitoring network."},
}

func main() {
	ctx := context.Background()
	official, err := catalog.Fetch(ctx, catalog.OfficialListURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "official list:", err)
		os.Exit(1)
	}
	fmt.Printf("official: %d projects\n", len(official))

	var extra []catalog.Project
	for _, p := range community {
		var cfg *catalog.Config
		for try := 0; try < 3 && cfg == nil; try++ {
			c, err := catalog.FetchConfig(ctx, p.URL)
			if err == nil {
				cfg = c
			} else if try == 2 {
				fmt.Printf("  skip %-22s %v\n", p.Name, err)
			} else {
				time.Sleep(2 * time.Second)
			}
		}
		if cfg == nil {
			continue
		}
		p.Source = "community"
		p.WebURL = p.URL
		p.Platforms = cfg.Platforms
		fmt.Printf("  keep %-22s %d platforms\n", p.Name, len(p.Platforms))
		extra = append(extra, p)
	}

	all := catalog.Merge(official, extra)
	data, err := json.MarshalIndent(all, "", " ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile("internal/catalog/projects.json", append(data, '\n'), 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("wrote internal/catalog/projects.json: %d projects\n", len(all))
}

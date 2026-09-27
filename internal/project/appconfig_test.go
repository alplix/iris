package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAppConfig(t *testing.T) {
	dir := t.TempDir()
	if ac, err := LoadAppConfig(dir); ac != nil || err != nil {
		t.Fatalf("no file must mean (nil, nil), got %v %v", ac, err)
	}
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, "app_config.xml"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// The real, documented format (lib/cc_config.cpp), including the
	// app_version/gpu_versions elements Iris does not act on yet.
	write(`<app_config>
 <app>
  <name>hsgamma_FGRP5</name>
  <max_concurrent>2</max_concurrent>
  <gpu_versions>
   <gpu_usage>1</gpu_usage>
   <cpu_usage>0.5</cpu_usage>
  </gpu_versions>
 </app>
 <app_version>
  <app_name>hsgamma_FGRP5</app_name>
  <plan_class>cuda</plan_class>
  <avg_ncpus>0.5</avg_ncpus>
  <ngpus>1</ngpus>
 </app_version>
 <project_max_concurrent>6</project_max_concurrent>
 <report_results_immediately/>
</app_config>`)
	ac, err := LoadAppConfig(dir)
	if err != nil || ac == nil {
		t.Fatalf("valid file rejected: %v", err)
	}
	if ac.ProjectMaxConcurrent != 6 || ac.Apps["hsgamma_FGRP5"] != 2 {
		t.Errorf("parsed wrongly: %+v", ac)
	}
	if _, ok := ac.Apps["some_other_app"]; ok {
		t.Error("an app the file never mentions must not appear")
	}

	// Only project_max_concurrent, no per-app entries.
	write(`<app_config><project_max_concurrent>3</project_max_concurrent></app_config>`)
	ac, err = LoadAppConfig(dir)
	if err != nil || ac.ProjectMaxConcurrent != 3 || len(ac.Apps) != 0 {
		t.Errorf("got %+v %v", ac, err)
	}

	// An <app> with no name is not usable and must not panic or match "".
	write(`<app_config><app><max_concurrent>1</max_concurrent></app></app_config>`)
	ac, err = LoadAppConfig(dir)
	if err != nil || len(ac.Apps) != 0 {
		t.Errorf("a nameless app must be skipped, got %+v %v", ac, err)
	}

	write(`not xml at all`)
	if _, err := LoadAppConfig(dir); err == nil {
		t.Error("unparseable content must be an error")
	}
}

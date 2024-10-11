// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package htmlutil

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Note: See testdata/show.go for preparing new entries in this table.
var splitTests = []struct {
	file     string
	sections []Section
}{
	{"testdata/basic.html",
		[]Section{
			{"First Heading", "first", "Section 1."},
			{"First Heading > Second Heading", "second", "Section 2."},
			{"First Heading > Second Heading > Third Heading", "third", "Section 3.\nMultiple lines."},
			{"First Heading > Fourth Heading", "fourth", "Section 4."},
		},
	},
	{"testdata/trace.html",
		[]Section{
			{"More powerful Go execution traces > Issues", "issues", "often be out of reach"},
			{"More powerful Go execution traces > Low-overhead tracing", "low-overhead-tracing", "Prior to Go 1.21"},
			{"More powerful Go execution traces > Scalable traces", "scalable-traces", "Go 1.22+ programs is now feasible."},
			{"More powerful Go execution traces > Flight recording", "flight-recording", "feedback, positive or negative"},
			{"More powerful Go execution traces > Trace reader API", "trace-reader-api", "an effort to clean up"},
			{"More powerful Go execution traces > Thank you!", "thank-you", "no small part"},
		},
	},
	{"testdata/toolchain.html",
		[]Section{
			{"Go Toolchains > Introduction", "intro", "Starting in Go 1.21, the Go"},
			{"Go Toolchains > Go versions", "version", "Released versions of Go use the"},
			{"Go Toolchains > Go toolchain names", "name", "The standard Go toolchains are named"},
			{"Go Toolchains > Module and workspace configuration", "config", "Go modules and workspaces specify"},
			{"Go Toolchains > The GOTOOLCHAIN setting", "GOTOOLCHAIN", "The go command selects the Go"},
			{"Go Toolchains > Go toolchain selection", "select", "At startup, the go command selects"},
			{"Go Toolchains > Go toolchain switches", "switch", "For most commands, the workspace’s"},
			{"Go Toolchains > Downloading toolchains", "download", "When using GOTOOLCHAIN=auto or"},
			{"Go Toolchains > Managing Go version module requirements with go get", "get", "In general the go command treats"},
			{"Go Toolchains > Managing Go version workspace requirements with go work", "work", "As noted in the previous section,"},
		},
	},
	{"testdata/release.html",
		[]Section{
			{"Go Wiki: Go-Release-Cycle > Overview", "overview", "Go is released every six months. Each r"},
			{"Go Wiki: Go-Release-Cycle > Timeline", "timeline", "The current release cycle is aligned to"},
			{"Go Wiki: Go-Release-Cycle > Timeline > January / July week 1: Planning for release begins.", "january--july-week-1-planning-for-release-begins", "Planning of major work for upcoming rel"},
			{"Go Wiki: Go-Release-Cycle > Timeline > January / July week 3: Release work begins.", "january--july-week-3-release-work-begins", "Once the prior release has entered its "},
			{"Go Wiki: Go-Release-Cycle > Timeline > May / November week 4: Release freeze begins.", "may--november-week-4-release-freeze-begins", "This milestone begins the second part o"},
			{"Go Wiki: Go-Release-Cycle > Timeline > June / December week 2: Release candidate 1 issued.", "june--december-week-2-release-candidate-1-issued", "A release candidate is meant to be as c"},
			{"Go Wiki: Go-Release-Cycle > Timeline > July / January week 3: Work on the next release begins", "july--january-week-3-work-on-the-next-release-begins", "While the current release is being stab"},
			{"Go Wiki: Go-Release-Cycle > Timeline > August / February week 2: Release issued.", "august--february-week-2-release-issued", "Finally, the release itself!\nA release "},
			{"Go Wiki: Go-Release-Cycle > Release Maintenance", "release-maintenance", "A minor release is issued to address on"},
			{"Go Wiki: Go-Release-Cycle > Freeze Exceptions", "freeze-exceptions", "Fix CLs that are\npermitted by the freez"},
			{"Go Wiki: Go-Release-Cycle > Historical note", "historical-note", "A version of this schedule, with a shor"},
		},
	},
	{"testdata/cmdgo.html",
		[]Section{
			{"Command go > Start a bug report", "hdr-Start_a_bug_report", "Usage:\ngo bug\n\nBug opens the default bro"},
			{"Command go > Compile packages and dependencies", "hdr-Compile_packages_and_dependencies", "Usage:\ngo build [-o output] [build flags"},
			{"Command go > Remove object files and cached files", "hdr-Remove_object_files_and_cached_files", "Usage:\ngo clean [-i] [-r] [-cache] [-tes"},
			{"Command go > Show documentation for package or symbol", "hdr-Show_documentation_for_package_or_symbol", "Usage:\ngo doc [doc flags] [package|[pack"},
			{"Command go > Print Go environment information", "hdr-Print_Go_environment_information", "Usage:\ngo env [-json] [-changed] [-u] [-"},
			{"Command go > Update packages to use new APIs", "hdr-Update_packages_to_use_new_APIs", "Usage:\ngo fix [-fix list] [packages]\n\nFi"},
			{"Command go > Gofmt (reformat) package sources", "hdr-Gofmt__reformat__package_sources", "Usage:\ngo fmt [-n] [-x] [packages]\n\nFmt "},
			{"Command go > Generate Go files by processing source", "hdr-Generate_Go_files_by_processing_source", "Usage:\ngo generate [-run regexp] [-n] [-"},
			{"Command go > Add dependencies to current module and install them", "hdr-Add_dependencies_to_current_module_and_install_them", "Usage:\ngo get [-t] [-u] [-v] [build flag"},
			{"Command go > Compile and install packages and dependencies", "hdr-Compile_and_install_packages_and_dependencies", "Usage:\ngo install [build flags] [package"},
			{"Command go > List packages or modules", "hdr-List_packages_or_modules", "Usage:\ngo list [-f format] [-json] [-m] "},
			{"Command go > Module maintenance", "hdr-Module_maintenance", "Go mod provides access to operations on "},
			{"Command go > Download modules to local cache", "hdr-Download_modules_to_local_cache", "Usage:\ngo mod download [-x] [-json] [-re"},
			{"Command go > Edit go.mod from tools or scripts", "hdr-Edit_go_mod_from_tools_or_scripts", "Usage:\ngo mod edit [editing flags] [-fmt"},
			{"Command go > Print module requirement graph", "hdr-Print_module_requirement_graph", "Usage:\ngo mod graph [-go=version] [-x]\n\n"},
			{"Command go > Initialize new module in current directory", "hdr-Initialize_new_module_in_current_directory", "Usage:\ngo mod init [module-path]\n\nInit i"},
			{"Command go > Add missing and remove unused modules", "hdr-Add_missing_and_remove_unused_modules", "Usage:\ngo mod tidy [-e] [-v] [-x] [-diff"},
			{"Command go > Make vendored copy of dependencies", "hdr-Make_vendored_copy_of_dependencies", "Usage:\ngo mod vendor [-e] [-v] [-o outdi"},
			{"Command go > Verify dependencies have expected content", "hdr-Verify_dependencies_have_expected_content", "Usage:\ngo mod verify\n\nVerify checks that"},
			{"Command go > Explain why packages or modules are needed", "hdr-Explain_why_packages_or_modules_are_needed", "Usage:\ngo mod why [-m] [-vendor] package"},
			{"Command go > Workspace maintenance", "hdr-Workspace_maintenance", "Work provides access to operations on wo"},
			{"Command go > Edit go.work from tools or scripts", "hdr-Edit_go_work_from_tools_or_scripts", "Usage:\ngo work edit [editing flags] [go."},
			{"Command go > Initialize workspace file", "hdr-Initialize_workspace_file", "Usage:\ngo work init [moddirs]\n\nInit init"},
			{"Command go > Sync workspace build list to modules", "hdr-Sync_workspace_build_list_to_modules", "Usage:\ngo work sync\n\nSync syncs the work"},
			{"Command go > Add modules to workspace file", "hdr-Add_modules_to_workspace_file", "Usage:\ngo work use [-r] [moddirs]\n\nUse p"},
			{"Command go > Make vendored copy of dependencies", "hdr-Make_vendored_copy_of_dependencies", "Usage:\ngo work vendor [-e] [-v] [-o outd"},
			{"Command go > Compile and run Go program", "hdr-Compile_and_run_Go_program", "Usage:\ngo run [build flags] [-exec xprog"},
			{"Command go > Manage telemetry data and settings", "hdr-Manage_telemetry_data_and_settings", "Usage:\ngo telemetry [off|local|on]\n\nTele"},
			{"Command go > Test packages", "hdr-Test_packages", "Usage:\ngo test [build/test flags] [packa"},
			{"Command go > Run specified go tool", "hdr-Run_specified_go_tool", "Usage:\ngo tool [-n] command [args...]\n\nT"},
			{"Command go > Print Go version", "hdr-Print_Go_version", "Usage:\ngo version [-m] [-v] [file ...]\n\n"},
			{"Command go > Report likely mistakes in packages", "hdr-Report_likely_mistakes_in_packages", "Usage:\ngo vet [build flags] [-vettool pr"},
			{"Command go > Build constraints", "hdr-Build_constraints", "A build constraint, also known as a buil"},
			{"Command go > Build modes", "hdr-Build_modes", "The 'go build' and 'go install' commands"},
			{"Command go > Calling between Go and C", "hdr-Calling_between_Go_and_C", "There are two different ways to call bet"},
			{"Command go > Build and test caching", "hdr-Build_and_test_caching", "The go command caches build outputs for "},
			{"Command go > Environment variables", "hdr-Environment_variables", "The go command and the tools it invokes "},
			{"Command go > File types", "hdr-File_types", "The go command examines the contents of "},
			{"Command go > The go.mod file", "hdr-The_go_mod_file", "A module version is defined by a tree of"},
			{"Command go > GOPATH environment variable", "hdr-GOPATH_environment_variable", "The Go path is used to resolve import st"},
			{"Command go > GOPATH and Modules", "hdr-GOPATH_and_Modules", "When using modules, GOPATH is no longer "},
			{"Command go > Internal Directories", "hdr-Internal_Directories", "Code in or below a directory named \"inte"},
			{"Command go > Vendor Directories", "hdr-Vendor_Directories", "Go 1.6 includes support for using local "},
			{"Command go > Module proxy protocol", "hdr-Module_proxy_protocol", "A Go module proxy is any web server that"},
			{"Command go > Import path syntax", "hdr-Import_path_syntax", "An import path (see 'go help packages') "},
			{"Command go > Relative import paths", "hdr-Relative_import_paths", "An import path beginning with ./ or ../ "},
			{"Command go > Remote import paths", "hdr-Remote_import_paths", "Certain import paths also\ndescribe how t"},
			{"Command go > Import path checking", "hdr-Import_path_checking", "When the custom import path feature desc"},
			{"Command go > Modules, module versions, and more", "hdr-Modules__module_versions__and_more", "Modules are how Go manages dependencies."},
			{"Command go > Module authentication using go.sum", "hdr-Module_authentication_using_go_sum", "When the go command downloads a module z"},
			{"Command go > Package lists and patterns", "hdr-Package_lists_and_patterns", "Many commands apply to a set of packages"},
			{"Command go > Configuration for downloading non-public code", "hdr-Configuration_for_downloading_non_public_code", "The go command defaults to downloading m"},
			{"Command go > Testing flags", "hdr-Testing_flags", "The 'go test' command takes both flags t"},
			{"Command go > Testing functions", "hdr-Testing_functions", "The 'go test' command expects to find te"},
			{"Command go > Controlling version control with GOVCS", "hdr-Controlling_version_control_with_GOVCS", "The 'go get' command can run version con"},
		},
	},
}

func TestSplit(t *testing.T) {
	for _, tt := range splitTests {
		t.Run(filepath.Base(tt.file), func(t *testing.T) {
			data, err := os.ReadFile(tt.file)
			if err != nil {
				t.Fatal(err)
			}
			sections := slices.Collect(Split(data))
			want := tt.sections
			for _, s := range sections {
				if len(want) == 0 {
					t.Fatalf("unexpected section Title=%q ID=%q, want end", s.Title, s.ID)
				}
				if s.Title != want[0].Title || s.ID != want[0].ID {
					t.Fatalf("unexpected section Title=%q ID=%q, want %q, %q", s.Title, s.ID, want[0].Title, want[0].ID)
				}
				if !strings.Contains(s.Text, want[0].Text) {
					t.Fatalf("section Title=%q ID=%q: want %q as substring; have:\n%s", s.Title, s.ID, want[0].Text, s.Text)
				}
				want = want[1:]
			}
			if len(want) > 0 {
				t.Fatalf("missing section Title=%q ID=%q", want[0].Title, want[0].ID)
			}

			for range Split(data) {
				// Test that stopping works.
				// If Split keeps yielding, the runtime will panic.
				break
			}
		})
	}
}

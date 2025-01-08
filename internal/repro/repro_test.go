// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package repro

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/oscar/internal/github"
	"golang.org/x/oscar/internal/llm"
	"golang.org/x/oscar/internal/testutil"
)

// TestCheckReproduction tests the basic behavior of CheckReproduction.
// This isn't a great test as it doesn't reach out to the LLM.
func TestCheckReproduction(t *testing.T) {
	ctx := context.Background()
	lg := testutil.Slogger(t)
	cgen := llm.TestContentGenerator("reprotest",
		func(ctx context.Context, schema *llm.Schema, promptParts []llm.Part) (string, error) {
			return testGenerateContent(t, ctx, schema, promptParts)
		},
	)
	caseTester := testCaseTester{t}

	if id, err := CheckReproduction(ctx, lg, cgen, caseTester, testIssue); err != nil {
		t.Error(err)
	} else if id == "" {
		t.Error("no bisection attempted")
	}
}

// testIssue is a test issue we pass to CheckReproduction.
// This is Go issue #70468, which happens to include a reproduction case
// that we can bisect.
var testIssue = &github.Issue{
	URL:              "https://api.github.com/repos/golang/go/issues/70468",
	HTMLURL:          "https://github.com/golang/go/issues/70468",
	Number:           70468,
	User:             github.User{Login: "guidovranken"},
	Title:            "crypto/ecdsa: Sign() panics if public key is not set",
	CreatedAt:        "2024-11-20T19:04:37Z",
	UpdatedAt:        "2024-11-20T19:25:12Z",
	ClosedAt:         "",
	Body:             testIssueBody,
	Assignees:        []github.User{github.User{Login: "FiloSottile"}},
	Milestone:        github.Milestone{Title: "Go1.24"},
	State:            "open",
	PullRequest:      (*struct{})(nil),
	Locked:           false,
	ActiveLockReason: "",
	Labels:           []github.Label{},
}

// testIssueBody is the body of go issue #70468.
// The fmt.Sprintf replaces %1[s] with a backquote,
// since Go backquoted strings can't themselves contain backquotes.
var testIssueBody = fmt.Sprintf(`### Go version

go version devel go1.24-7c7170e Wed Nov 20 18:27:31 2024 +0000 linux/amd64

### Output of %[1]sgo env%[1]s in your module/workspace:

%[1]s%[1]s%[1]sshell
AR='ar'
CC='clang-15'
CGO_CFLAGS='-O2 -g'
CGO_CPPFLAGS=''
CGO_CXXFLAGS='-O2 -g'
CGO_ENABLED='1'
CGO_FFLAGS='-O2 -g'
CGO_LDFLAGS='-O2 -g'
CXX='clang++-15'
GCCGO='gccgo'
GO111MODULE=''
GOAMD64='v1'
GOARCH='amd64'
GOAUTH='netrc'
GOBIN=''
GOCACHE='/home/jhg/.cache/go-build'
GODEBUG=''
GOENV='/home/jhg/.config/go/env'
GOEXE=''
GOEXPERIMENT=''
GOFIPS140='off'
GOFLAGS=''
GOGCCFLAGS='-fPIC -m64 -pthread -fno-caret-diagnostics -Qunused-arguments -Wl,-no-gc-sections -fmessage-length=0 -ffile-prefix-map=/tmp/go-build1928511716=/tmp/go-build -gno-record-gcc-switches'
GOHOSTARCH='amd64'
GOHOSTOS='linux'
GOINSECURE=''
GOMOD='/home/jhg/oss-fuzz-379773077/p/go.mod'
GOMODCACHE='/home/jhg/oss-fuzz-379773077/go-dev/packages/pkg/mod'
GONOPROXY=''
GONOSUMDB=''
GOOS='linux'
GOPATH='/home/jhg/oss-fuzz-379773077/go-dev/packages'
GOPRIVATE=''
GOPROXY='https://proxy.golang.org,direct'
GOROOT='/home/jhg/oss-fuzz-379773077/go-dev'
GOSUMDB='sum.golang.org'
GOTELEMETRY='local'
GOTELEMETRYDIR='/home/jhg/.config/go/telemetry'
GOTMPDIR=''
GOTOOLCHAIN='auto'
GOTOOLDIR='/home/jhg/oss-fuzz-379773077/go-dev/pkg/tool/linux_amd64'
GOVCS=''
GOVERSION='devel go1.24-7c7170e Wed Nov 20 18:27:31 2024 +0000'
GOWORK=''
PKG_CONFIG='pkg-config'
%[1]s%[1]s%[1]s


### What did you do?

%[1]s%[1]s%[1]sgo
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"math/big"
)

func main() {
	priv, ok := new(big.Int).SetString("123", 10)
	if ok == false {
		panic("Cannot decode bignum")
	}
	var priv_ecdsa ecdsa.PrivateKey
	priv_ecdsa.D = priv
	priv_ecdsa.PublicKey.Curve = elliptic.P256()

	msg := "hello, world"
	hash := sha256.Sum256([]byte(msg))

	r, s, err := ecdsa.Sign(rand.Reader, &priv_ecdsa, hash[:])
	fmt.Println(err)
	fmt.Println(r.String())
	fmt.Println(s.String())
}
%[1]s%[1]s%[1]s

### What did you see happen?

%[1]s%[1]s%[1]s
panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=0x1 addr=0x10 pc=0x4c2117]

goroutine 1 [running]:
math/big.(*Int).Sign(...)
	/home/jhg/oss-fuzz-379773077/go-dev/src/math/big/int.go:48
crypto/ecdsa.pointFromAffine({0x56bc88?, 0x646070?}, 0x0, 0x0)
	/home/jhg/oss-fuzz-379773077/go-dev/src/crypto/ecdsa/ecdsa.go:402 +0x37
crypto/ecdsa.privateKeyToFIPS[...](0xc0002d02c0, 0xc0002dff18)
	/home/jhg/oss-fuzz-379773077/go-dev/src/crypto/ecdsa/ecdsa.go:391 +0x38
crypto/ecdsa.signFIPS[...](0xc0002d02c0, 0xc0002c8df8, {0x56b020?, 0x66c280}, {0xc0002dfed8, 0x20, 0x20})
	/home/jhg/oss-fuzz-379773077/go-dev/src/crypto/ecdsa/ecdsa.go:234 +0x46
crypto/ecdsa.SignASN1({0x56b020, 0x66c280}, 0xc0002dff18, {0xc0002dfed8, 0x20, 0x20})
	/home/jhg/oss-fuzz-379773077/go-dev/src/crypto/ecdsa/ecdsa.go:220 +0x229
crypto/ecdsa.Sign({0x56b020?, 0x66c280?}, 0xbf7cbadf074d6483?, {0xc0002c8ed8?, 0xc0002c8ef8?, 0xc?})
	/home/jhg/oss-fuzz-379773077/go-dev/src/crypto/ecdsa/ecdsa_legacy.go:60 +0x37
main.main()
	/home/jhg/oss-fuzz-379773077/p/x.go:23 +0xf4
%[1]s%[1]s%[1]s

### What did you expect to see?

No panic and output like:

%[1]s%[1]s%[1]s
<nil>
40753909606936490861524166827361514810969838335424734688996005945565630866707
3003946056421339230760555414094068582110502790139132375823642300394845145720
%[1]s%[1]s%[1]s`,
	"`")

// testIssueRepro is the reproduction case that the LLM extracts from
// the body.
var testIssueRepro = `package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"math/big"
)

func main() {
	priv, ok := new(big.Int).SetString("123", 10)
	if ok == false {
		panic("Cannot decode bignum")
	}
	var priv_ecdsa ecdsa.PrivateKey
	priv_ecdsa.D = priv
	priv_ecdsa.PublicKey.Curve = elliptic.P256()

	msg := "hello, world"
	hash := sha256.Sum256([]byte(msg))

	r, s, err := ecdsa.Sign(rand.Reader, &priv_ecdsa, hash[:])
	fmt.Println(err)
	fmt.Println(r.String())
	fmt.Println(s.String())
}`

// testGenerateContent is a testing implementation of an LLM that
// extracts the test case.
func testGenerateContent(t *testing.T, ctx context.Context, schema *llm.Schema, promptParts []llm.Part) (string, error) {
	if len(promptParts) == 0 {
		return "", errors.New("testGenerateContent: no input")
	}

	text, ok := promptParts[0].(llm.Text)
	if !ok {
		return "", fmt.Errorf("testGenerateContent got part type %T, expected text", promptParts[0])
	}

	if strings.Contains(string(text), "Your job is to categorize Go issues.") {
		// Called for label categorization.
		// TODO(iant): Remove if we pass labels in.
		type responseType struct {
			CategoryName string
			Explanation  string
		}
		response := &responseType{
			CategoryName: "bug",
			Explanation:  "This is a bug.",
		}
		out, err := json.Marshal(response)
		if err != nil {
			return "", err
		}
		return string(out), nil

	}

	var response *reproResponse
	if !strings.Contains(string(text), "crypto/ecdsa: Sign() panics if public key is not set") {
		t.Error("testGenerateContent returning unknown")
		response = &reproResponse{
			Repro:       "unknown",
			FailRelease: "unknown",
			PassRelease: "unknown",
		}
	} else {
		t.Log("testGenerateContent recognized test case")
		response = &reproResponse{
			Repro:       testIssueRepro,
			FailRelease: "go1.24",
			PassRelease: "go1.23",
		}
	}

	out, err := json.Marshal(response)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// testCaseTester is an implementation of [CaseTester] for a single test.
type testCaseTester struct {
	t *testing.T
}

// Clean cleans up the test case.
func (tct testCaseTester) Clean(ctx context.Context, body string) (string, error) {
	if body != testIssueRepro {
		return "", fmt.Errorf("testCaseTester.Clean: unexpected test case %q", body)
	}
	return body, nil
}

// CleanVersions cleans up the suggested versions.
func (tct testCaseTester) CleanVersions(ctx context.Context, pass, fail string) (string, string) {
	if pass != "go1.23" && fail != "go1.24" {
		tct.t.Errorf("got versions %q, %q, want %q, %q", pass, fail, "go1.23", "go1.24")
		return "unknown", "unknown"
	}
	return pass, fail
}

// Try runs a cleaned test at the suggested versions
// and reports whether it passed or failed.
func (testCaseTester) Try(ctx context.Context, body, version string) (bool, error) {
	if body != testIssueRepro {
		return false, fmt.Errorf("testCaseTester.Try: unexpected test case %q", body)
	}
	switch version {
	case "go1.23":
		return true, nil
	case "go1.24":
		return false, nil
	default:
		return false, fmt.Errorf("testCaseTester.Try: unexpected version %q", version)
	}
}

// Bisect bisects the test case.
func (tct testCaseTester) Bisect(ctx context.Context, issue *github.Issue, body, pass, fail string) (string, error) {
	if issue.Number != testIssue.Number {
		return "", fmt.Errorf("testCaseTester.Bisect: unexpected issue %d", issue.Number)
	}
	if body != testIssueRepro {
		return "", fmt.Errorf("testCaseTester.Bisect: unexpected test case %q", body)
	}
	if pass != "go1.23" || fail != "go1.24" {
		return "", fmt.Errorf("testCaseTester.Bisect: unexpected versions %q, %q", pass, fail)
	}

	// The correct answer here is git revision
	// 6f5194767ea032853b3f3e4cf008fbeec5c61945
	// aka https://go.dev/cl/628676.
	// But we don't have a to report that yet.

	tct.t.Log("in production we would start bisecting")

	return "bisection-identifier", nil
}

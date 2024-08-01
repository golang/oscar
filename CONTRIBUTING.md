# Contributing to Go

Go is an open source project.

It is the work of hundreds of contributors. We appreciate your help!

## Filing issues

When [filing an issue](https://golang.org/issue/new), make sure to answer these five questions:

1.  What version of Go are you using (`go version`)?
2.  What operating system and processor architecture are you using?
3.  What did you do?
4.  What did you expect to see?
5.  What did you see instead?

General questions should go to the [golang-nuts mailing list](https://groups.google.com/group/golang-nuts) instead of the issue tracker.
The gophers there will answer or ask you to file an issue if you've tripped over a bug.

## Contributing code

Please read the [Contribution Guidelines](https://golang.org/doc/contribute.html)
before sending patches.

Unless otherwise noted, the Go source files are distributed under
the BSD-style license found in the LICENSE file.

## Developing code

This repo consists of two modules:

  - The top-level one, golang.org/x/oscar
  - A nested module, golang.org/x/oscar/internal/gcp, for packages that
    depend on Google Cloud Platform.

If you work on both together, you should have a `go.work` file at the repo root
with the contents
```
go 1.23

use (
	.
	./internal/gcp
)
```
Do not commit this file, or the related `go.work.sum` file that will be created
automatically, into the repo.
To make git ignore them for this repo, add these lines to your `.git/info/exclude` file:
```
go.work
go.work.sum
```

# Parser tests

This directory contains tests of the AIP 160 parser.

The tests are in
[txtar](https://pkg.go.dev/golang.org/x/tools/txtar).

The txtar files alternate between tests and test results. A test will
be named something like "star.test". It will contain a string,
possibly multiple lines, to pass to `Parse`.  A "star.test" entry will
be followed by either "star.out" or "star.err".

A "star.out" entry means that `Parse` is expected to succeed. Printing
the resulting `Expr`, using the `String` method, should match the
contents of "star.out".

A "star.err" entry means that `Parse` is expected to fail. The `Error`
method of the resulting error should match the contents of "star.err".

In the case of changes to the parser, the txtar files may be easily
regenerated to match the current code by running

    go test -test.run=TestParse -update

You can update the contents of a single txtar file by naming the file
in the `-test.run` option, as in

    go test -test.run=TestParse/regexp -update

Of course, after using `-update`, carefully check any changes to the
.txt files to make sure that the tests are still valid.

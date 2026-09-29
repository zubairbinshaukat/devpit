# Robocopy fixtures

These logs are **constructed, not recorded**. They were written by hand from
knowledge of robocopy's output format and of how each Windows language
translates it. No German, French, Japanese or Turkish machine produced them.

What is faithful, because the parser depends on it:

- the shape of a file line (two tabs, a size in bytes, a tab, a full path),
  which is what `/BYTES /NC /FP /NJH /NDL /NP` produce;
- the shape of an error line (date, time, one translated word, the error
  number, its hex form in brackets, a translated sentence, a path);
- the summary table: a header row of six translated column names, then three
  rows of six whole numbers, then a times row and speed rows;
- the way each language writes its date, its label colons (French puts a space
  before the colon) and its non-ASCII text.

What is not verified: the exact translated words, and the exact column
padding. The parser never reads the words and never depends on the padding,
which is the point of the test. If a recording from a real machine turns up,
add it next to these and add its name to the table in `parse_test.go`.

`copy_*.log` is a copy with one failed file (error 112, disk full) followed by
a retry line and one more finished file. `dry_*.log` is a dry run with a
5 GB file, a file whose destination path passes 260 characters, and a file
that lives in the destination and not the source (an extra).

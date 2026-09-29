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

## Recorded fixtures

`recorded_en_*.unilog` are **recorded, not constructed**: the byte-exact
`/UNILOG` files robocopy wrote on Windows 11 (English, build 26200), run with
exactly the flags `CopyArgs` and `DryRunArgs` build, between two local
folders. The only edit is the folder prefix, replaced with `C:\rec`. They are
UTF-16 little endian with a byte order mark and CRLF line ends, as robocopy
writes them, so Git sees them as binary and never rewrites them.

- `recorded_en_dry.unilog`: `/L` dry run; the destination holds an extra file
  and an older copy of one file; a junction back to the source is skipped by
  `/XJ`. Exit code 3.
- `recorded_en_copy_locked.unilog`: the copy, with one destination file held
  open by another program, `/R:1 /W:1`. Exit code 11.
- `recorded_en_resume.unilog`: the same copy run again. Exit code 3.
- `recorded_en_fatal.unilog`: a source folder that does not exist. Exit 16.
- `recorded_en_mt_big.unilog`: a 3 GB file and a small one with `/MT:16`. The
  log was polled while it ran: the big file's line appeared only when it had
  finished.

What the recordings showed that the constructed files did not:

- a file's line is written even when its copy fails, just before its error
  line, and a retry writes the line again;
- with several threads, "Waiting 1 seconds..." and " Retrying..." are glued to
  the end of whatever line came last, file lines included;
- a file that is only in the destination is listed like a copied file, with
  its destination path;
- the error line of a failed copy names the **source** path ("Copying File
  C:\rec\src\locked.txt"). The constructed fixtures use a destination path,
  which robocopy prints for destination operations.

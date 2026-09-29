# net view and net use fixtures

These outputs are **constructed, not recorded**. They were written by hand
from knowledge of the layout of `net view \\host` and `net use`, with the
words translated the way each Windows language does it. No German, French,
Japanese or Turkish machine produced them.

What is faithful, because the parser depends on it: a title line, a header
row, a line of dashes, one row per share with the name, the type and an
optional comment separated by runs of spaces, then a closing sentence. The
hidden administrative share `C$` is in every file, as it is in `net view /all`.

What is not verified: the exact translated words and the exact padding. The
parser reads the dashed line and the gaps between columns, never a word.
If a recording from a real machine turns up, add it next to these.

# The frozen case set

`cases.jsonl` is authored by hand in `author_cases.py`, which is the only thing here that
cannot be regenerated from the code. Regenerate the file after editing the table:

```
python3 eval/author_cases.py
```

## The labelling rule

Three labels, and the rule decides every argument in advance:

| label | means | action |
|---|---|---|
| `retryable` | a transient condition in the infrastructure | retry later, nobody needs to look |
| `expected` | normal operation that happens to look like an error | nothing to do |
| `needs_human` | it will not fix itself | someone has to read it |

Two boundaries need spelling out, because they are where the labels disagree with the
words:

1. **Who closed the connection.** `connection reset by peer` is `retryable` - the
   infrastructure dropped it. `client disconnected` is `expected` - the client got what it
   wanted and left. Same words, opposite meaning.
2. **Will waiting help?** `429 Too Many Requests` is `retryable`. `429 … quota exceeded for
   the month` is `needs_human`, because waiting will not help. The status code is not the
   signal; the reason is.

## Why a third of the cases are marked hard

A case with a `why` is one where the obvious keyword reading is wrong: a configuration dump
containing "timeout", a completed migration reporting "nothing to do", a `permission denied`
buried after a timeout. The evaluation reports the score on those separately, because a
headline number over a mostly-easy set is the most common way an evaluation flatters itself.

## What this case set is not

It is **mine**, it is **small** (~120 inputs), and I wrote both the rules and the labels.
A model that scores well on it is not thereby good at triage; what the repository
demonstrates is the *measurement* - frozen cases, a baseline, a CI gate that fails on a
regression, and a cost number - and the honest limits of a case set this size are part of
the result rather than a footnote to it.

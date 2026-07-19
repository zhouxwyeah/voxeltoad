import { commonPrefixLength, messagesOf, type TraceDetailLike } from "./trace-categories";

/**
 * Picks the best "previous" baseline from a set of candidate requests on the
 * session timeline, used to derive the carried-over vs. new message split.
 *
 * Background: a session timeline is not a single conversation. Claude Code's
 * Task/Explore subagents share the parent's session_id, so their requests are
 * interleaved with the main agent's on the timeline. Taking "the previous row"
 * blindly as the diff baseline frequently picks a subagent row whose system
 * prompt differs — `commonPrefixLength` then collapses to 0 and the whole
 * request gets mislabelled as "all new".
 *
 * Instead we score every candidate by how long a common message prefix it
 * shares with the current request, and keep the best. Subagent rows score 0
 * (different system message) and are skipped naturally; the closest ancestor
 * on the same branch wins. When *every* candidate scores 0 (current is the
 * first request, or genuinely starts a new branch), we return null so the
 * caller renders everything as new — which is correct for a fresh turn.
 *
 * `candidates` should be ordered most-recent-first (closest row first). Ties
 * favour the earliest entry, i.e. the closest row, since a longer recent
 * prefix is the stronger "I grew from here" signal.
 */
export function pickBaseline<T extends TraceDetailLike>(
  current: TraceDetailLike,
  candidates: T[],
): T | null {
  const curMsgs = messagesOf(current);
  if (curMsgs.length === 0) return null;

  let best: T | null = null;
  let bestLen = 0;
  for (const c of candidates) {
    const len = commonPrefixLength(curMsgs, messagesOf(c));
    // Strictly greater: on a tie, keep the earlier (closer) candidate. For
    // normally-growing branches the closest ancestor also has the longest
    // prefix, so this is just defensive against equal-prefix siblings.
    if (len > bestLen) {
      bestLen = len;
      best = c;
    }
  }
  return best;
}

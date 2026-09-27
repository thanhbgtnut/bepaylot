package wiki

// The wiki has one prompt: extraction. Pages, links, the index and the log
// are written by code from the index tree and what the LLM extracted, so an
// ingest costs one call per file (one per part of a big file). The prompt is
// in English; the marker "extract entities" identifies it (tests script it).

const promptExtract = `You extract entities and relations from a business file to extract entities for the wiki of one case
(an "LLM Wiki": sources are immutable and every fact in the wiki cites its source line). Wiki language: %s.
Conventions of this wiki:
%s
Entity types (only these; identity attributes identify one entity):
%s
Relations between entity types:
%s
File lines are given as [p<page>:L<line>] text.

Reply with JSON only, no prose:
{"entities": [{"id": "e1", "type": "<entity type>", "name": "<name as written>", "aliases": ["..."], "role": "<its role in this file, max 8 words>", "at": "p<page>:L<line>",
   "attributes": {"<attribute>": {"value": <value as written>, "at": "p<page>:L<line>"}}}],
 "relations": [{"from": "e1", "to": "e2", "type": "<relation>", "at": "p<page>:L<line>"}]}
Rules:
- Only what the file states. Copy identifiers, amounts, dates and names exactly as written.
- "at" is the line holding the value (p<page>:L<a>-<b> when it spans lines); an entity's "at" is where its name is.
- "role" says what the entity is in this file (e.g. "bên giao thầu", "chủ tài khoản nhận tiền"), in the wiki language.
- Only attributes and relations of the schema. At most %d entities; skip entities with no name.`

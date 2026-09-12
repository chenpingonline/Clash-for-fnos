export interface ChangelogEntry { version: string; date: string; body: string }

export function parseChangelog(markdown: string): ChangelogEntry[] {
  const entries: ChangelogEntry[] = []
  const headings = [...markdown.matchAll(/^## \[([^\]]+)\](?:\s*-\s*([^\r\n]+))?\s*$/gm)]
  for (const [index, heading] of headings.entries()) {
    entries.push({
      version: heading[1]!,
      date: heading[2]?.trim() || '',
      body: markdown.slice(heading.index! + heading[0].length, headings[index + 1]?.index ?? markdown.length).trim(),
    })
  }
  return entries
}

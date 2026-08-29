'use strict';

/** @param {string} line */
function yamlIndentWidth(line) {
  let count = 0;
  for (const char of String(line || '')) {
    if (char === ' ') count += 1;
    else if (char === '\t') count += 2;
    else break;
  }
  return count;
}

/** @param {unknown} raw @param {boolean} [flowStyle] */
function parseYamlNameScalar(raw, flowStyle = false) {
  let value = String(raw || '').trim();
  if (!value) return '';
  const quote = value[0];
  if (quote === "'" || quote === '"') {
    let output = '';
    for (let index = 1; index < value.length; index += 1) {
      const char = value[index];
      if (quote === "'" && char === "'" && value[index + 1] === "'") { output += "'"; index += 1; continue; }
      if (char === quote) return output;
      if (quote === '"' && char === '\\' && index + 1 < value.length) {
        const next = value[index += 1];
        const escapes = { n: '\n', r: '\r', t: '\t', '"': '"', '\\': '\\' };
        output += Object.prototype.hasOwnProperty.call(escapes, next) ? escapes[/** @type {keyof typeof escapes} */ (next)] : next;
      } else output += char;
    }
    return output.trim();
  }
  if (flowStyle) {
    const comma = value.indexOf(',');
    const brace = value.indexOf('}');
    let end = value.length;
    if (comma >= 0) end = Math.min(end, comma);
    if (brace >= 0) end = Math.min(end, brace);
    value = value.slice(0, end);
  }
  return value.replace(/\s+#.*$/, '').trim();
}

/** @param {unknown} raw */
function parseProxyGroupOrder(raw) {
  const lines = String(raw || '').split(/\r?\n/);
  let headerIndex = -1;
  let baseIndent = 0;
  for (let index = 0; index < lines.length; index += 1) {
    const match = lines[index].match(/^(\s*)(?:proxy-groups|'proxy-groups'|"proxy-groups")\s*:\s*(.*)$/);
    if (!match) continue;
    headerIndex = index;
    baseIndent = yamlIndentWidth(match[1]);
    if (String(match[2] || '').trim() && !String(match[2] || '').trim().startsWith('#')) return [];
    break;
  }
  if (headerIndex < 0) return [];
  const order = [];
  const seen = new Set();
  for (let index = headerIndex + 1; index < lines.length; index += 1) {
    const line = lines[index];
    if (!line.trim() || /^\s*#/.test(line)) continue;
    const indent = yamlIndentWidth(line);
    if (indent <= baseIndent) break;
    const trimmed = line.trim();
    if (!trimmed.startsWith('-')) continue;
    let after = trimmed.slice(1).trim();
    const flowStyle = after.startsWith('{');
    if (flowStyle) after = after.slice(1).trim();
    const match = after.match(/^name\s*:\s*(.*)$/);
    if (!match) continue;
    const name = parseYamlNameScalar(match[1], flowStyle);
    if (name && !seen.has(name)) { seen.add(name); order.push(name); }
  }
  return order;
}

module.exports = { parseProxyGroupOrder, parseYamlNameScalar, yamlIndentWidth };

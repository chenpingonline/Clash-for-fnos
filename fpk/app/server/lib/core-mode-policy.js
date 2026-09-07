'use strict';

function resolveStartupCoreMode({ requestedMode = null, externalAvailable = false } = {}) {
  const requested = String(requestedMode || '').trim().toLowerCase();
  if (requested === 'external' || requested === 'managed') {
    return { mode: requested, requiresChoice: false };
  }
  if (externalAvailable) return { mode: 'auto', requiresChoice: true };
  return { mode: 'managed', requiresChoice: false };
}

module.exports = { resolveStartupCoreMode };

'use strict';

const { DEFAULT_DNS_SETTINGS, normalizeDnsSettings } = require('./dns-settings');

function defaultDnsOverrideSettings() {
  return normalizeDnsSettings(DEFAULT_DNS_SETTINGS);
}

/** @param {unknown} input */
function normalizeStoredDnsOverride(input) {
  try { return normalizeDnsSettings(input || DEFAULT_DNS_SETTINGS); }
  catch (_) { return defaultDnsOverrideSettings(); }
}

/**
 * @param {unknown} currentEnabled
 * @param {unknown} currentSettings
 * @param {unknown} input
 */
function resolveDnsOverrideUpdate(currentEnabled, currentSettings, input) {
  const body = input && typeof input === 'object' && !Array.isArray(input)
    ? /** @type {Record<string, unknown>} */ (input)
    : {};
  const hasDns = Object.prototype.hasOwnProperty.call(body, 'dns');
  const hasEnabled = Object.prototype.hasOwnProperty.call(body, 'dnsOverrideEnabled');
  const enabled = hasEnabled ? Boolean(body.dnsOverrideEnabled) : currentEnabled === true;
  const settings = hasDns
    ? normalizeDnsSettings(body.dns, normalizeStoredDnsOverride(currentSettings))
    : normalizeStoredDnsOverride(currentSettings);
  return {
    enabled,
    settings,
    shouldApply: enabled && (hasDns || hasEnabled)
  };
}

module.exports = {
  defaultDnsOverrideSettings,
  normalizeStoredDnsOverride,
  resolveDnsOverrideUpdate
};

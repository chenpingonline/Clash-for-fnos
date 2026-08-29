'use strict';

/** @typedef {Record<string, unknown>} JsonObject */

/**
 * Reject arrays and primitive request bodies before they reach privileged code.
 * @param {unknown} value
 * @param {string} [label]
 * @returns {JsonObject}
 */
function requireObject(value, label = '请求参数') {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw badRequest(`${label}必须是对象`);
  }
  return /** @type {JsonObject} */ (value);
}

/**
 * @param {unknown} value
 * @param {string} name
 * @param {{required?: boolean, maxLength?: number, pattern?: RegExp, trim?: boolean}} [options]
 */
function requireString(value, name, options = {}) {
  if (value === undefined || value === null) {
    if (options.required) throw badRequest(`${name}不能为空`);
    return '';
  }
  if (typeof value !== 'string') throw badRequest(`${name}必须是字符串`);
  const result = options.trim === false ? value : value.trim();
  if (options.required && !result) throw badRequest(`${name}不能为空`);
  if (options.maxLength && result.length > options.maxLength) throw badRequest(`${name}长度不能超过 ${options.maxLength}`);
  if (options.pattern && result && !options.pattern.test(result)) throw badRequest(`${name}格式不正确`);
  return result;
}

/** @param {unknown} value @param {string} name */
function requireBoolean(value, name) {
  if (typeof value !== 'boolean') throw badRequest(`${name}必须是布尔值`);
  return value;
}

/**
 * @param {unknown} value
 * @param {string} name
 * @param {{min?: number, max?: number, optional?: boolean}} [options]
 */
function requireInteger(value, name, options = {}) {
  if ((value === undefined || value === null || value === '') && options.optional) return null;
  const number = typeof value === 'number' ? value : Number(value);
  if (!Number.isInteger(number)) throw badRequest(`${name}必须是整数`);
  if (options.min !== undefined && number < options.min) throw badRequest(`${name}不能小于 ${options.min}`);
  if (options.max !== undefined && number > options.max) throw badRequest(`${name}不能大于 ${options.max}`);
  return number;
}

/** @param {unknown} value @param {string} [name] */
function requireTransactionId(value, name = '事务 ID') {
  return requireString(value, name, { required: true, maxLength: 128, pattern: /^[a-zA-Z0-9._-]+$/ });
}

/** @param {string} message */
function badRequest(message) {
  return Object.assign(new Error(message), { statusCode: 400 });
}

module.exports = {
  badRequest,
  requireBoolean,
  requireInteger,
  requireObject,
  requireString,
  requireTransactionId
};

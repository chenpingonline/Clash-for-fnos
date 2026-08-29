// @ts-check

export function createPageLifecycle() {
  let revision = 0;
  /** @type {AbortController | null} */
  let controller = null;

  return {
    /** @param {string} name */
    begin(name) {
      controller?.abort();
      controller = new AbortController();
      const currentRevision = revision += 1;
      const signal = controller.signal;
      return {
        name,
        signal,
        isCurrent: () => currentRevision === revision && !signal.aborted
      };
    },
    cancel() {
      revision += 1;
      controller?.abort();
      controller = null;
    }
  };
}

/** @param {unknown} error */
export function isAbortError(error) {
  return error instanceof DOMException
    ? error.name === 'AbortError'
    : Boolean(error && typeof error === 'object' && /** @type {{name?: unknown}} */ (error).name === 'AbortError');
}

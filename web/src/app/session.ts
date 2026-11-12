import { signal } from '@preact/signals';

/** True once a request answered 401: this device's pairing is gone, and the shell says so without a reload. */
export const signedOut = signal(false);

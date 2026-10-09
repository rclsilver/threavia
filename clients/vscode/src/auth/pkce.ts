import { createHash, randomBytes } from 'node:crypto';

/**
 * Proof Key for Code Exchange (RFC 7636), the S256 method.
 *
 * The extension is a public client: anything shipped in a .vsix can be read,
 * so it holds no secret. PKCE is what replaces one: the provider refuses a code
 * that does not come back with the verifier matching the challenge it saw, and
 * only this process ever held the verifier.
 */

/** A verifier of 43 to 128 characters from the unreserved set, as the RFC asks. */
export function createVerifier(bytes = 64): string {
  return base64url(randomBytes(bytes));
}

/** URL-safe base64 of the SHA-256 of the verifier, which is S256. */
export function challengeOf(verifier: string): string {
  return base64url(createHash('sha256').update(verifier, 'ascii').digest());
}

/** An opaque value tying a callback to the sign-in that started it. */
export function createState(bytes = 24): string {
  return base64url(randomBytes(bytes));
}

export function base64url(bytes: Uint8Array): string {
  return Buffer.from(bytes).toString('base64').replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

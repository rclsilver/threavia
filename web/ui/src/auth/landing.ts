/**
 * Where the person was going when the sign-in interrupted them.
 *
 * A module of its own so the session provider stays a file of components: the
 * callback route reads this, and the provider writes it, and neither needs the
 * other.
 */
let landing = '/';

export function setLandingPath(path: string) {
  landing = path;
}

export function landingPath(): string {
  return landing;
}

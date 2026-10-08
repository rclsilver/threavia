/**
 * Whether this page is the demo: the client under /demo, answering itself with
 * fictitious data, with no sign-in and no server behind it.
 */
export const DEMO = typeof window !== 'undefined' && /^\/demo(\/|$)/.test(window.location.pathname);

import { createContext, useContext } from 'react';

/**
 * The Project the sidebar is pointing at.
 *
 * A Session or a Project view names its own in the URL; the views that do not —
 * the dashboard — need the same answer the sidebar is showing, or they would
 * ask the reader to choose something they have already chosen.
 */
export const SelectedProject = createContext<string | undefined>(undefined);

export function useSelectedProject(): string | undefined {
  return useContext(SelectedProject);
}

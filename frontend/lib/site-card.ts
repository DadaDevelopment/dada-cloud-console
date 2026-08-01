/**
 * Open Graph summary of a deployed app's page, as produced by
 * `/api/site-card` and rendered by the console's preview pane.
 */
export interface SiteCard {
  url: string;
  /** HTTP status of the fetch; `0` when the app could not be reached at all. */
  status: number;
  title?: string;
  description?: string;
  /** Absolute https URL on a platform host, or absent. */
  image?: string;
  siteName?: string;
}

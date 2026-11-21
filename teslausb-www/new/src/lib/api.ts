/**
 * Thin wrapper around fetch() that attaches the CSRF token required by all
 * mutating endpoints (/cgi-bin/rm.sh, reboot.sh, …).
 *
 * The token is read from the teslausb_csrf cookie that any GET to a read
 * endpoint plants (e.g. ls.sh, status). No cookie → empty string → backend 403.
 */
export function getCSRFToken(): string {
  const m = document.cookie.match(/(?:^|;\s*)teslausb_csrf=([^;]+)/)
  return m ? decodeURIComponent(m[1]) : ''
}

export async function apiFetch(url: string, options: RequestInit = {}): Promise<Response> {
  return fetch(url, {
    ...options,
    headers: {
      ...options.headers,
      'X-CSRF-Token': getCSRFToken(),
    },
  })
}

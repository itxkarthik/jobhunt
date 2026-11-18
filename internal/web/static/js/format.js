// Formatting shared by the views. Mirrors the CLI where it has an
// equivalent (app.Since for ages).

// since is a compact age like the CLI's: <1h, 5h, 3d, 2w.
export function since(iso, now = Date.now()) {
  const t = Date.parse(iso);
  if (!iso || Number.isNaN(t) || t <= 0) return "?";
  const hours = (now - t) / 3.6e6;
  if (hours < 1) return "<1h";
  if (hours < 48) return `${Math.floor(hours)}h`;
  if (hours < 14 * 24) return `${Math.floor(hours / 24)}d`;
  return `${Math.floor(hours / 24 / 7)}w`;
}

export function dateTime(iso) {
  const t = Date.parse(iso);
  return Number.isNaN(t) ? "" : new Date(t).toLocaleString();
}

export function date(iso) {
  const t = Date.parse(iso);
  return Number.isNaN(t) ? "" : new Date(t).toLocaleDateString();
}

const PLACES = { "remote-global": "Remote, global" };

// place names a country code: "remote-global" or "region:apac" read
// better spelled out.
export function place(code) {
  if (PLACES[code]) return PLACES[code];
  if (code.startsWith("region:")) return code.slice(7).toUpperCase();
  return code;
}

export function location(job) {
  return job.location || job.city || (job.countries || []).map(place).join(", ") || "unknown";
}

export function salary(job) {
  if (job.salary_min == null && job.salary_max == null) return "";
  const n = (v) => (v == null ? "?" : Math.round(v).toLocaleString());
  const range = job.salary_min === job.salary_max || job.salary_max == null
    ? n(job.salary_min ?? job.salary_max)
    : `${n(job.salary_min)}–${n(job.salary_max)}`;
  return job.currency ? `${range} ${job.currency}` : range;
}

export function visa(job) {
  if (job.visa_sponsorship === true) return "sponsors visas";
  if (job.visa_sponsorship === false) return "no sponsorship";
  return "";
}

export const statusLabel = {
  saved: "Saved", applied: "Applied", oa: "OA", interview: "Interview", offer: "Offer", rejected: "Rejected",
};

// shortDate is "12 Nov".
export function shortDate(iso) {
  const t = Date.parse(iso);
  return Number.isNaN(t) ? "" : new Date(t).toLocaleDateString(undefined, { day: "2-digit", month: "short" });
}

// clock is "20:29:04".
export function clock(t) {
  return new Date(t).toLocaleTimeString(undefined, { hour12: false });
}

// ago is "just now", "4m ago", "3h ago", "2d ago".
export function ago(iso, now = Date.now()) {
  const t = typeof iso === "number" ? iso : Date.parse(iso);
  if (!t || Number.isNaN(t)) return "never";
  const s = Math.max(0, (now - t) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 48 * 3600) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

export function count(n) {
  return Number(n || 0).toLocaleString();
}

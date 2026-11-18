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

export function location(job) {
  return job.location || job.city || (job.countries || []).join(", ") || "unknown";
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

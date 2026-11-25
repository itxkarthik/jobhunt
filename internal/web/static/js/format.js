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

// SHORT and placeLabel mirror placeLabel in internal/model/where.go.
const SHORT = {
  IN: "India", US: "USA", JP: "Japan", GB: "UK", DE: "Germany",
  CA: "Canada", SG: "Singapore", NL: "Netherlands", AU: "Australia", AE: "UAE",
  "remote-global": "Worldwide",
};

function placeLabel(code) {
  if (SHORT[code]) return SHORT[code];
  if (code.startsWith("region:")) return code.slice(7).toUpperCase();
  return code;
}

// shortLocation mirrors model.ShortLocation: a job open in several countries
// reads "India +17", naming the first searched country (want) it lists,
// instead of a long alphabetical list that hides that country.
export function shortLocation(job, want = []) {
  const cs = job.countries || [];
  if (job.city) return job.city;
  if (cs.length > 1) return `${placeLabel(want.find((w) => cs.includes(w)) || cs[0])} +${cs.length - 1}`;
  return job.location || cs.map(placeLabel).join(", ");
}

export function location(job) {
  return job.location || job.city || (job.countries || []).map(place).join(", ") || "unknown";
}

const SIGNS = { INR: "₹", USD: "$", EUR: "€", GBP: "£", JPY: "¥" };
const PER = { year: "/yr", month: "/mo", week: "/wk", day: "/day", hour: "/hr" };

// compact writes 15000 as "15k", 1500000 as "1.5M", and lakhs for rupees
// ("12L"), as model.Job.Pay does.
function compact(v, inr) {
  let unit = "", div = 1;
  if (inr && v >= 1e7) [unit, div] = ["Cr", 1e7];
  else if (inr && v >= 1e5) [unit, div] = ["L", 1e5];
  else if (v >= 1e6) [unit, div] = ["M", 1e6];
  else if (v >= 1e3) [unit, div] = ["k", 1e3];
  const n = v / div;
  let prec = n < 100 && Math.abs(n - Math.round(n)) >= 0.05 ? 1 : 0;
  if (div === 1 && n < 100 && n !== Math.trunc(n) && Math.abs(n * 100 - Math.round(n * 100)) < 1e-6) prec = 2;
  return n.toFixed(prec) + unit;
}

// pay is a job's pay, like "₹15k–20k/mo", "$40/hr", "unpaid" or "paid";
// "" when the posting doesn't say. It matches model.Job.Pay.
export function pay(job) {
  let lo = job.salary_min, hi = job.salary_max;
  if (lo == null && hi == null) return job.paid === true ? "paid" : job.paid === false ? "unpaid" : "";
  if (lo == null || (hi != null && lo === 0)) lo = hi;
  if (hi == null || hi === lo) hi = null;
  const code = String(job.currency || "").toUpperCase();
  const sign = SIGNS[code] || (code ? `${code} ` : "");
  const inr = code === "INR";
  return `${sign}${compact(lo, inr)}${hi != null ? `–${compact(hi, inr)}` : ""}${PER[job.pay_period || "year"] || "/yr"}`;
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

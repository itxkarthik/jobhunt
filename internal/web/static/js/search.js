// The search view arrives in a later commit.
import { h } from "./dom.js";

export function mount(main) {
  main.append(h("p", { class: "empty" }, "This view isn't built yet."));
}

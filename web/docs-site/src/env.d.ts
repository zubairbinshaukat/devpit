/// <reference path="../.astro/types.d.ts" />

declare namespace App {
  interface Locals {
    /** Filled by routeData.ts from the `related` frontmatter array. */
    devpitRelated?: { href: string; title: string; description: string }[];
  }
}

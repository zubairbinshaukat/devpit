import { defineCollection } from 'astro:content';
import { docsLoader } from '@astrojs/starlight/loaders';
import { docsSchema } from '@astrojs/starlight/schema';
import { z } from 'astro/zod';
import { blogSchema } from 'starlight-blog/schema';

/**
 * Frontmatter added on top of Starlight's own fields and starlight-blog's.
 * Every field is optional; see CONTRACT.md for how each one is used.
 */
const devpitFields = z.object({
  /** Q&A pairs. Emits FAQPage JSON-LD. Answers are plain text, no Markdown. */
  faq: z
    .array(z.object({ question: z.string().min(5), answer: z.string().min(10) }))
    .optional(),
  /** Slugs (content ids, e.g. "features/settings") linked in a Related block at the page end. */
  related: z.array(z.string()).optional(),
  /** JSON-LD type for the page. Defaults: troubleshooting pages TechArticle, everything else none. */
  schemaType: z.enum(['TechArticle', 'Article', 'none']).optional(),
  /** Publication date (YYYY-MM-DD). Used as datePublished in Article JSON-LD. */
  published: z.date().optional(),
  /**
   * Short <title> and og:title for a page whose H1 (`title`) is long, such as
   * a full error message. It must start with the part people search for.
   */
  seoTitle: z.string().max(70).optional(),
  /** Text shown on the generated social image instead of the page title (keep it under about 60 characters). */
  ogTitle: z.string().max(80).optional(),
});

export const collections = {
  docs: defineCollection({
    loader: docsLoader(),
    schema: docsSchema({
      extend: (context) => blogSchema(context).merge(devpitFields),
    }),
  }),
};

export type BlogPost = {
  slug: string;
  path: string;
  title: string;
  h1: string;
  description: string;
  date: string;
  readingMinutes: number;
  keywords: string[];
  teaser: string;
};

export const BLOG_POSTS: BlogPost[] = [
  {
    slug: 'how-to-build-an-ai-agent',
    path: '/blog/how-to-build-an-ai-agent/',
    title: 'How to Build an AI Agent: Step-by-Step Guide (2026)',
    h1: 'How to build an AI agent: a step-by-step guide for production',
    description: 'How to build an AI agent in five steps — design, grounding, tools, guardrails, and shipping — with a no-code path and code-framework alternatives.',
    date: '2026-07-14',
    readingMinutes: 11,
    keywords: ['how to build an AI agent', 'how to build AI agents', 'build AI agents', 'no-code AI agent builder'],
    teaser: 'Most agent tutorials stop where production starts. This one covers the full path: designing the agent team, grounding it in your data, giving it tools safely, and shipping it behind an API — with or without writing code.',
  },
  {
    slug: 'what-are-ai-agents',
    path: '/blog/what-are-ai-agents/',
    title: 'What Are AI Agents? Types, Use Cases, How They Work',
    h1: 'What are AI agents? Types, use cases, and how they actually work',
    description: 'What are AI agents, in plain English: how they reason and act, the main types of AI agents, real use cases, and when your product actually needs one.',
    date: '2026-05-20',
    readingMinutes: 9,
    keywords: ['what are AI agents', 'types of AI agents', 'AI agent use cases', 'how AI agents work'],
    teaser: 'A plain-English guide: what separates an agent from a chatbot, the types that matter in practice, what agents can actually do today, and when your product needs one.',
  },
  {
    slug: 'dify-alternative-open-source-ai-agents',
    path: '/blog/dify-alternative-open-source-ai-agents/',
    title: 'Dify Alternative: SyntheticBrew vs Dify Compared (2026)',
    h1: 'Dify alternative: SyntheticBrew vs Dify for production AI agents',
    description: 'Looking for a Dify alternative? Compared in detail: Go runtime vs 13-container stack, multi-tenant licensing, per-tool confirmation, pricing, migration.',
    date: '2026-06-24',
    readingMinutes: 12,
    keywords: ['Dify alternative', 'SyntheticBrew vs Dify', 'Dify pricing', 'what is Dify', 'best AI agent platform'],
    teaser: 'Dify is a capable AI app studio. But if you are embedding agents into your own product — multi-tenant, permission-aware, auditable — the architecture and the license both start working against you. A detailed breakdown.',
  },
  {
    slug: 'langchain-vs-langgraph',
    path: '/blog/langchain-vs-langgraph/',
    title: 'LangChain vs LangGraph: When to Use Which (2026)',
    h1: 'LangChain vs LangGraph: what actually differs, and when to use which',
    description: 'LangChain vs LangGraph explained: chains vs stateful graphs, where each fits, migration notes — and the production runtime layer neither framework gives you.',
    date: '2026-06-05',
    readingMinutes: 8,
    keywords: ['LangChain vs LangGraph', 'LangGraph vs LangChain', 'LangChain alternatives', 'AI agent framework'],
    teaser: 'Same team, two very different tools. A practical guide to choosing between LangChain and LangGraph — and a clear look at the runtime work that remains after you pick either.',
  },
];

export const BLOG_MAP = Object.fromEntries(BLOG_POSTS.map((post) => [post.slug, post])) as Record<string, BlogPost>;

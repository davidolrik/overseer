import { execSync } from "node:child_process";
import { defineConfig } from "vitepress";
import { withMermaid } from "vitepress-plugin-mermaid";

const version = execSync("git describe --tags --abbrev=0").toString().trim();

// https://vitepress.dev/reference/site-config
export default withMermaid(
    defineConfig({
        title: "Overseer",
        titleTemplate: "Overseer",
        description: "SSH Tunnel Manager with contextual awareness",
        appearance: "force-dark",
        head: [
            [
                "link",
                {
                    rel: "preconnect",
                    href: "https://fonts.googleapis.com",
                },
            ],
            [
                "link",
                {
                    rel: "preconnect",
                    href: "https://fonts.gstatic.com",
                    crossorigin: "",
                },
            ],
            [
                "link",
                {
                    rel: "stylesheet",
                    href: "https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;500;700&display=swap",
                },
            ],
            [
                "link",
                {
                    rel: "icon",
                    type: "image/svg+xml",
                    href: "/favicon.svg",
                },
            ],
            ["meta", { name: "author", content: "David Jack Wange Olrik" }],
            [
                "meta",
                {
                    name: "keywords",
                    content:
                        "overseer, ssh, tunnel, manager, context, awareness, network, security",
                },
            ],
            ["meta", { property: "og:type", content: "website" }],
        ],
        themeConfig: {
            logo: "/overseer.png",
            nav: [
                { text: "Guide", link: "/guide/what-is-overseer" },
                { text: "Advanced", link: "/advanced/shell-integration" },
                {
                    text: version,
                    link: `https://github.com/davidolrik/overseer/releases/tag/${version}`,
                },
            ],

            sidebar: [
                {
                    text: "Guide",
                    items: [
                        {
                            text: "What is Overseer?",
                            link: "/guide/what-is-overseer",
                        },
                        {
                            text: "Installation",
                            link: "/guide/installation",
                        },
                        { text: "Quick Start", link: "/guide/quick-start" },
                        {
                            text: "Authentication",
                            link: "/guide/authentication",
                        },
                        {
                            text: "Configuration",
                            link: "/guide/configuration",
                        },
                        { text: "Commands", link: "/guide/commands" },
                    ],
                },
                {
                    text: "Advanced",
                    items: [
                        {
                            text: "Shell Integration",
                            link: "/advanced/shell-integration",
                        },
                        {
                            text: "SSH ControlMaster",
                            link: "/advanced/ssh-controlmaster",
                        },
                        {
                            text: "ProxyJump",
                            link: "/advanced/proxy-jump",
                        },
                        {
                            text: "Dynamic Tunnels",
                            link: "/advanced/dynamic-tunnels",
                        },
                        {
                            text: "Companion Scripts",
                            link: "/advanced/companion-scripts",
                        },
                    ],
                },
            ],
            socialLinks: [
                {
                    icon: "github",
                    link: "https://github.com/davidolrik/overseer",
                },
            ],
            search: {
                provider: "local",
            },
        },
        cleanUrls: true,
        assetsDir: "static",
    }),
);

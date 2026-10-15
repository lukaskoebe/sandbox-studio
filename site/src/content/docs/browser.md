---
title: Browser
description: Give a teammate a controlled Chromium browser without exposing its debugging port.
---

Each teammate has its own Chromium browser running in a separate microVM. Agents control it with browser tools; they do not receive a remote debugging port. Cookies and logins belong to that teammate's browser profile.

![A teammate's Browser tab: the browser window with its activity beside it](../../assets/screenshots/browser.jpg)

## Approve actions and access

Read-only actions such as snapshots and reading text are allowed. Opening a page still follows the sandbox's network rules. Actions that change a page, such as clicking or filling a field, ask for approval unless an allow rule matches. Uploads and downloads always ask. You can deny actions or add rules for an origin and action.

Script execution, cookie access, storage access, and page state inspection are not available to the agent tools. Password-like and marked-sensitive fields are redacted from snapshots. When you approve **Fill credential**, Studio fills a host-side secret into the page without showing the value to the agent.

Browser network requests use the environment's network rules. When an agent drives the browser, requests use the driving sandbox's policy. See [Network rules](../network/) and [Secrets](../secrets/).

## Watch and take the wheel

The browser is one window that looks like any browser: tabs, Back, Forward, Reload, an address bar and Downloads. It is in three places:

- the teammate's **Browser** tab, with **Activity** and **Sites** beside it;
- beside a teammate's sandbox, from **Browser** in the workspace toolbar;
- beside the teammate's conversation, from **Watch** on the browsing card the thread shows while the teammate browses.

The line under the toolbar always says who drives. To use the page yourself, type an address and press Enter, press Back or Reload, or choose **Take the wheel**: the teammate's browser calls wait until you choose **Hand back**. When the teammate asks to click or fill something, the question shows on the page with its answers.

The shield in the address bar shows what the teammate may do on the site: **Ask first**, **Allowed** or **Blocked** for clicks and typing, and which fields are hidden from it. **Sites** lists every site with a rule or a visit, and **Add a rule or hide a field** covers single actions and fields. **Activity** lists every step by session; click one to see the page as it was after it.

A browser sleeps after a while unused and wakes when the teammate browses or you open a page. **Reset profile** in the options menu signs the teammate out of every site.

## Throwaway sessions

An agent can open a throwaway session for a task that should not leave cookies or logins behind. A throwaway session starts with an empty profile. It ends when the agent closes it, after 10 minutes without use, when the sandbox that opened it stops or is deleted, when the chat that opened it is deleted, or when the teammate's browser stops. Studio knows the chat only when it was the sandbox's one running chat. A session you have taken over stays until you hand it back. Its cookies, storage and downloads are discarded with it. A teammate can have two at a time, across its sandboxes.

Approvals work as they do for the teammate's own browser. Approval cards name the session. Each open throwaway session is a tab of the browser window, after the teammate's own, so you can watch one, take it over, or end it with its tab's close button.

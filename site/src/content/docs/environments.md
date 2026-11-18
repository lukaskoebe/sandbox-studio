---
title: Environments
description: Keep work, personal projects, and their access policies separate.
---

An environment is a separate workspace inside Studio. Use the switcher at the top of the sidebar to move between environments or create another one. To rename one, change its name under **Settings → General**; its computers and links stay as they are.

To delete one, use **Delete…** on the same page and type its name. Studio stops and removes its computers with their disks, and deletes its templates, teammates, chats, memory, providers, Git forges, secrets and rules. It can't be undone, so export the environment first if you may want its configuration back. While a computer is busy, an import runs or a template builds, Studio refuses and says why. The last environment can't be deleted.

Each environment has its own:

- computers and templates;
- network rules, and the requests that wait on them;
- secrets and AI providers;
- teammates, Git forges, and memory.

For example, keep work credentials and network rules in a **Work** environment and personal projects in **Private**. A computer belongs to one environment, and an agent can use only that environment's providers, secrets, rules, and memory. The sidebar's **Environment** group, with **Computers**, **Network**, **Secrets**, **Memory** and **Activity**, always shows the environment you are in.

In **Settings**, **This environment** and **Accounts** (AI providers and Git forges) belong to the environment you are in, and so do the templates listed under **This computer**. The rest of **This computer**, the runtime, ports and the LAN, and updates, is for the whole of Studio, as are most of the **Studio** settings under **General**, such as Start at login, SSH, previews and the terminal shell. **This browser** holds the style, which each browser keeps for itself.

![The environment switcher open at the top of the sidebar, listing Default and Private](../../assets/screenshots/environments.jpg)

## Scoping a rule

Network rules can apply to every computer in an environment, one teammate's computers, or one computer. A teammate rule follows the teammate's ownership; it does not grant access to another teammate's computers. An environment-level deny acts as a fence and takes precedence over narrower allows.

See [Network rules](../network/) for rule actions and [Teammates and providers](../teammates-providers/) for which teammate a computer belongs to.

## Export and import an environment

To set up the same environment on another Studio, or share it, choose **Export environment** under **Settings → General**. The `.studio-environment` file holds the environment's providers, teammates, environment and teammate rules, secret names with their hosts, and template specs. It leaves out provider keys, secret values, forge tokens, sandboxes, sandbox-scoped rules and secrets, memory, chats, approvals, and built templates. Template setup scripts and souls are in the file as you wrote them, so read it before you share it.

**Import environment** creates a new environment from such a file. Studio checks every value first, and an invalid file creates nothing. Afterwards it lists what the file left out: add each provider's key and each secret's value, and build the templates from their specs.

## Copy a teammate to another environment

To use the same teammate in another environment, open its settings (its name in the bar at the top, then **Settings**) and choose **Copy to another environment** under **Routine and access**. The copy keeps its name, role, avatar, soul, agent, model, git identity and approval setting. Its computers, chats, memory, secrets and keys stay where they are.

---
title: Limitations
description: The honest list of what this cannot do, stated plainly.
---

## History depth is decided by your phone

This is the one that surprises people. "Search my whole history" can turn out to
mean "search the last few months". Pairing with `--full-history` asks for more,
and `fetch_older_messages` pages further back one chat at a time, but the phone
decides what it actually sends. Neither this server nor you can override that.

## One client at a time on stdio

The data directory takes an exclusive lock. Use the
[http transport](/docs/transports) if you need more than one.

## No group management

It can read a group's members, subject, description and admins. It cannot create
groups, add or remove people, or change settings.

## Voice notes must already be Ogg Opus

`send_voice_note` does no transcoding and rejects other formats.

## No calls

Call history is readable. Making or answering calls is not supported.

## Sends are throttled, deliberately

Even after you approve, the rate limiter applies. This is not tunable up.

## It is a third-party client

Nothing here changes that, and it is the part of [the ban risk](/docs/ban-risk)
no amount of careful engineering can remove.

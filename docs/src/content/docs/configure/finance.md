---
title: Finance
description: Log spending by telling Eggy or sending a receipt photo, and see it in a Finance tab.
eyebrow: Configure
---

Enabling finance lets each person log what they spend by telling Eggy ("lunch 14.50") or by sending a photo of a receipt, correct or delete an entry afterwards, and see their spending in a **Finance** tab in the web panel.

Without this section there is no finance tool, no Finance tab, and nothing about it in any model request.

## Turn it on

In the panel, open **Settings → Capabilities**, switch on **Enable finance**, pick a default currency, and save. Then restart Eggy: finance is built once at startup, so a change applies on the next restart. The card says **Restart to apply** while the saved setting and the running one differ.

Or in `config.yaml`:

```yaml
finance:
  enabled: true
  currency: "SGD"
```

`currency` is what an entry takes when no currency is said. It defaults to `SGD`, and it is one of the ten Eggy supports.

## Currencies

Every entry carries its own currency, so a trip abroad can be logged in local currency: **USD, EUR, JPY, GBP, CNY, AUD, CAD, CHF, HKD, SGD.**

Totals are kept **per currency**. Nothing is converted, because that needs exchange rates that go stale, and a total that mixes SGD and JPY means nothing.

The currency is always its own field, never read from a symbol: ¥ is both yen and yuan, and $ is five of the ten. If an amount arrives as "$14", Eggy asks for the currency rather than guessing.

JPY has no minor unit, so `1200` is a valid yen amount and `1200.50` is refused. The other nine use two decimal places.

## Logging

Tell Eggy in chat:

> kopi 1.80
>
> coffee and a croissant, 9.40, Ya Kun

or send a receipt photo on Telegram. Eggy reads the image with its vision model, so the selected model must be able to read images (Eggy says so when it cannot). It logs the receipt's **total**, not each line, unless you ask for lines.

Eggy replies with what it logged and the entry's id, so a correction is one sentence:

> that receipt was actually 23.40

Photos work on Telegram. Web chat has no attachments yet, so there you describe the purchase in words.

If a receipt's total, currency or date is unclear, Eggy asks instead of guessing.

## The Finance tab

The tab appears only while finance is running. It shows, for one month at a time:

- a total per currency, your default first
- spending by category, as bars sized against the largest in each currency
- a day-by-day strip across the month
- every entry, which you can edit or delete in place
- an **Add entry** form, for what you did not tell Eggy

Use the arrows to move between months. A bookmark to `/finance` opens chat instead when finance is off.

## Privacy

Entries belong to the account that logged them. Another account cannot list, change or delete them, through the tab or through Eggy, and an id that is not yours is indistinguishable from one that does not exist.

Eggy's own prompt carries no entries. The model sees them only when it calls the tool in a turn you started.

## Approvals

`finance` is classified *internal*: every action writes, but only to your own account's entries, which you read and edit in the tab. In `normal` mode it runs without asking, so you are not tapping approve per coffee. In `strict` mode every call is put to you. A heartbeat or schedule cannot call it at all. See [Approvals](/eggy/use/approvals/).

## What it does not do

- No budgets, alerts or recurring entries.
- No refunds or income: every amount is a positive expense.
- No currency conversion.
- No import or export.
- Receipt photos are not kept as entries. Eggy reads the image during the turn and logs the entry; the conversation history keeps a note that an image was attached, not the image itself.

## Turning it off

Switch it off in the same card and restart. The tab, the routes and the tool disappear. **Your entries stay** in `eggy.db` and come back when you turn it on again.

## Cost when disabled

Nothing. With the section absent or `enabled: false`, nothing is constructed: no tool schema reaches a model request, no route is served, no tab is drawn and no goroutine runs. The only residue is an empty table in the database.

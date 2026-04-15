# Frontend Build

This directory contains the Tailwind CSS build pipeline for the embedded
`plane-feeder` web UI and a thin local prototype lane for fast page iteration.

Output:

- compiled stylesheet: `../internal/web/static/assets/app.css`
- prototype stylesheet: `./prototypes/assets/prototype.css`

Typical commands:

```bash
cd ps/frontend
npm install
npm run build:css
```

Watch mode during UI work:

```bash
npm run watch:css
```

Prototype workflow:

1. Build the prototype stylesheet with Tailwind.
2. Serve `ps/frontend/prototypes/` with a local static file server.
3. Iterate against fixture JSON first, then port the approved markup into
   `ps/internal/web/static/`.

Example:

```bash
cd ps/frontend
npm install
npm run build:prototype-css
python3 -m http.server 4173
```

Then open:

- `http://127.0.0.1:4173/prototypes/dashboard.html`
- `http://127.0.0.1:4173/prototypes/gps.html`
- `http://127.0.0.1:4173/prototypes/receiver.html`
- `http://127.0.0.1:4173/prototypes/diagnostics.html`

Notes:

- Prototypes intentionally reuse the same visual language and classes as the
  embedded UI, but Tailwind utility markup can evolve here first. They are not
  a separate frontend app.
- The fixture files live in `ps/frontend/prototypes/fixtures/`.

The Go web server serves and embeds the generated asset from
`ps/internal/web/static/assets/`.

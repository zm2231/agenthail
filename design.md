# Agenthail dashboard design

## Product intent

Agenthail is an operator workspace for the agents already running on a person's machine. The dashboard should answer three questions quickly: what is working, what needs attention, and where can I continue the conversation?

## Visual direction

- Genre: modern minimal
- Macrostructure: workbench
- Theme: warm graphite with a restrained coral accent
- Density: compact navigation, comfortable reading surfaces
- Personality: calm, capable, local, human

## Layout

- A compact product bar holds navigation and daemon health.
- Overview prioritizes attention and current work, then connected surfaces.
- Conversations use an inbox rail beside a continuous notebook transcript.
- The composer floats at the bottom of the notebook and expands with its content.
- Operations use labeled settings cards and a readable audit column.
- Mobile becomes one column with horizontal navigation and no clipped controls.

## Components

- Segmented controls for Active, Recent, and All conversation scopes.
- Borderless conversation list items with a coral selection rail.
- Continuous transcript turns with source labels and optional expansion for long content.
- Soft secondary actions and one coral primary action.
- Connected indicators appear beside connection copy. Counts do not masquerade as progress.
- Empty states are plain language inside the existing surface, not nested boxes.

## Type and color

- Display: Avenir Next, 700
- Body: Avenir Next, 400 to 600
- Code: SFMono-Regular
- Body text never drops below 14px.
- Colors use OKLCH tokens. Coral is reserved for selection, primary actions, and important live state.

## Interaction

- Successful background refreshes stay silent.
- Errors and queued delivery outcomes receive concise status messages.
- Hover and focus states use background and focus rings, not movement.
- Motion is limited to short opacity and background transitions.
- All controls remain keyboard accessible and have visible focus.

## Responsive behavior

- At tablet width, the conversation rail narrows and operations become one column.
- At mobile width, the rail and notebook stack, session lists are bounded, and the composer remains in normal document flow.
- No horizontal scrolling is permitted in the application shell or composer.

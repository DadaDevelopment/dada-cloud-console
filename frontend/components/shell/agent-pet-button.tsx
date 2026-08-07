"use client";

type AgentPetButtonProps = {
  onClick: () => void;
  title: string;
  ariaLabel: string;
  dataOnboarding?: string;
};

/**
 * Floating launcher for the agent chat: a cloud with a robot that peeks out
 * from behind it. Replaces the plain circular Bot-icon button that used to
 * sit here (fixed bottom-5 right-5 in the console shell) — same click
 * target and position, just with a bit more personality.
 */
export function AgentPetButton({ onClick, title, ariaLabel, dataOnboarding }: AgentPetButtonProps) {
  return (
    <button
      type="button"
      data-onboarding={dataOnboarding}
      onClick={onClick}
      title={title}
      aria-label={ariaLabel}
      className="agent-pet-btn fixed bottom-5 right-5 z-40"
    >
      <svg viewBox="0 0 340 340" className="agent-pet-svg" aria-hidden="true">
        <g className="agent-pet-robot">
          <g transform="translate(155,222)">
            <line x1="0" y1="-96" x2="0" y2="-80" stroke="var(--pet-outline)" strokeWidth="4" strokeLinecap="round" />
            <circle className="agent-pet-antenna-light" cx="0" cy="-100" r="4" fill="#f472b6" stroke="var(--pet-outline)" strokeWidth="2" />

            <rect x="-30" y="-80" width="60" height="46" rx="10" fill="var(--pet-robot-body)" stroke="var(--pet-outline)" strokeWidth="4" />
            <g className="agent-pet-eye">
              <rect x="-21" y="-64" width="14" height="14" rx="3" fill="var(--pet-robot-glow)" stroke="var(--pet-outline)" strokeWidth="3" />
              <rect x="7" y="-64" width="14" height="14" rx="3" fill="var(--pet-robot-glow)" stroke="var(--pet-outline)" strokeWidth="3" />
              <rect x="-16" y="-59" width="5" height="5" fill="var(--pet-outline)" />
              <rect x="12" y="-59" width="5" height="5" fill="var(--pet-outline)" />
            </g>
            <rect x="-38" y="-58" width="8" height="14" rx="3" fill="var(--pet-robot-body-dark)" stroke="var(--pet-outline)" strokeWidth="3" />
            <rect x="30" y="-58" width="8" height="14" rx="3" fill="var(--pet-robot-body-dark)" stroke="var(--pet-outline)" strokeWidth="3" />

            <rect x="-6" y="-34" width="12" height="8" fill="var(--pet-robot-body-dark)" stroke="var(--pet-outline)" strokeWidth="2.5" />

            <rect x="-36" y="-26" width="72" height="70" rx="10" fill="var(--pet-robot-body)" stroke="var(--pet-outline)" strokeWidth="4" />
            <rect x="-16" y="-14" width="32" height="24" rx="5" fill="var(--pet-robot-body-dark)" stroke="var(--pet-outline)" strokeWidth="3" />
            <circle cx="0" cy="-2" r="5" fill="var(--pet-robot-glow)" stroke="var(--pet-outline)" strokeWidth="2.5" />

            <rect x="-50" y="-14" width="16" height="18" rx="5" fill="var(--pet-robot-body)" stroke="var(--pet-outline)" strokeWidth="3.5" />
            <rect x="34" y="-14" width="16" height="18" rx="5" fill="var(--pet-robot-body)" stroke="var(--pet-outline)" strokeWidth="3.5" />
          </g>
        </g>

        <g className="agent-pet-float">
          <g transform="translate(5,10) scale(1.35)">
            <path
              fill="var(--pet-cloud)"
              stroke="var(--pet-cloud-stroke)"
              strokeWidth="5.2"
              strokeLinejoin="round"
              strokeLinecap="round"
              d="M55 190
                 C30 190 12 172 12 150
                 C12 130 26 114 46 111
                 C48 88 68 70 93 70
                 C108 70 121 77 130 88
                 C138 78 151 72 165 72
                 C190 72 210 92 211 116
                 C230 120 244 137 244 158
                 C244 180 226 197 204 197
                 L55 197 Z"
            />
          </g>
        </g>
      </svg>
    </button>
  );
}

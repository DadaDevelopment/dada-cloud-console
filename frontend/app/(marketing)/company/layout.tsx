import type { Metadata } from "next";

const SITE_URL = "https://cloud.dada-tuda.ru";
const TITLE = "Реквизиты";
const DESCRIPTION =
  'Реквизиты ООО "ДАДА ДЕВЕЛОПМЕНТ" — юридического лица, оказывающего услуги DADA Cloud: ИНН, КПП, ОГРН, юридический адрес и банковские реквизиты для оплаты по счёту.';

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  alternates: {
    canonical: "/company",
    languages: {
      "ru-RU": "/company",
      "en-US": "/en/company",
      "x-default": "/company",
    },
  },
  openGraph: {
    type: "website",
    url: `${SITE_URL}/company`,
    siteName: "DADA Cloud",
    title: TITLE,
    description: DESCRIPTION,
    locale: "ru_RU",
    alternateLocale: ["en_US"],
  },
  robots: { index: true, follow: true },
};

export default function CompanyLayout({ children }: { children: React.ReactNode }) {
  return children;
}

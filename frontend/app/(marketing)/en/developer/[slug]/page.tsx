import type { Metadata } from "next";
import { docMetadata } from "@/lib/docs";

export { default, generateStaticParams } from "../../../developer/[slug]/page";

export async function generateMetadata({ params }: { params: Promise<{ slug: string }> }): Promise<Metadata> {
  const { slug } = await params;
  return docMetadata(slug, "en");
}

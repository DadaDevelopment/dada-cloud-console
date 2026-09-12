import { ArrowUpRight, Cloud, GitBranch, Check, Database, Globe } from "lucide-react";
import styles from "./launch-scene.module.css";

export function LaunchScene({ en = false }: { en?: boolean }) {
  return <figure className={styles.scene} aria-label={en ? "From code to a running application: GitHub, cloud, database and HTTPS" : "От кода до приложения: GitHub, облако, база данных и HTTPS"}>
    <div className={styles.orbit} aria-hidden="true" />
    <div className={styles.orbitSmall} aria-hidden="true" />
    <div className={styles.code}><GitBranch size={20} aria-hidden="true" /><div><span>GitHub</span><strong>your-next-big-thing</strong></div><span className={styles.branch}>main</span></div>
    <div className={styles.connector} aria-hidden="true"><span /><span /><span /></div>
    <div className={styles.cloud}><Cloud strokeWidth={1.1} aria-hidden="true" /><span>DADA CLOUD</span></div>
    <div className={styles.database}><Database size={26} strokeWidth={1.5} aria-hidden="true" /><span>PostgreSQL</span></div>
    <div className={styles.live}><span className={styles.liveLabel}><span />{en ? "YOUR APP IS ONLINE" : "ВАШЕ ПРИЛОЖЕНИЕ В СЕТИ"}</span><strong>hello, world.<ArrowUpRight size={27} aria-hidden="true" /></strong><span className={styles.address}><Globe size={13} aria-hidden="true" /> HTTPS <Check size={13} aria-hidden="true" /></span></div>
    <figcaption>{en ? "You make the product. We help it run." : "Вы создаёте продукт. Мы помогаем ему работать."}</figcaption>
  </figure>;
}

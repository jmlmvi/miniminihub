# DESIGN-SYSTEM-SOCLEHUB -- Guide complet pour developpeurs TheSocle V005

> **Version** : 1.0 | **Date** : 2026-03-19
> **Stack** : Thymeleaf 3.x + Alpine.js 3.x + Tailwind CSS CDN
> **Prerequis** : Projet V005 avec `eu.lmvi:socle-v005:5.0.0`

---

## 1. Introduction

Ce document est la reference complete pour reproduire l'IHM SocleHub dans n'importe quel projet TheSocle V005. Chaque composant est accompagne de son code Thymeleaf + Alpine.js + Tailwind **pret a copier-coller**.

**Principes fondamentaux :**

- **Zero build frontend** : tout est servi depuis le JAR via Thymeleaf. Tailwind et Alpine.js sont charges via CDN.
- **Layout unique** : un seul `base.html` avec topbar, sidebar, contenu principal. Toutes les pages heritent de ce layout.
- **Workers = backend** : les controleurs Thymeleaf appellent directement les Workers V005 (meme JAR, pas d'appels REST).
- **Alpine.js pour l'interactivite** : pas de jQuery, pas de React. Alpine gere les toggles, modales, dropdowns.
- **Tailwind pour le style** : pas de CSS custom sauf exceptions documentees dans `portal.css`.

---

## 2. Mise en place dans un projet V005

### 2.1 Dependances Maven

Ajouter ces dependances au `pom.xml` du projet, en plus de celles deja presentes pour le Socle V005 :

```xml
<!-- Thymeleaf -->
<dependency>
    <groupId>org.springframework.boot</groupId>
    <artifactId>spring-boot-starter-thymeleaf</artifactId>
</dependency>

<!-- Thymeleaf Layout Dialect (heritage de layout) -->
<dependency>
    <groupId>nz.net.ultraq.thymeleaf</groupId>
    <artifactId>thymeleaf-layout-dialect</artifactId>
    <version>3.3.0</version>
</dependency>

<!-- Spring Security (authentification formulaire) -->
<dependency>
    <groupId>org.springframework.boot</groupId>
    <artifactId>spring-boot-starter-security</artifactId>
</dependency>
```

Ces dependances s'ajoutent au `pom.xml` minimal V005 qui inclut deja :
- `eu.lmvi:socle-v005:5.0.0`
- `spring-boot-starter-web` (avec exclusion Logback)
- `spring-boot-starter-log4j2`
- `com.lmax:disruptor:3.4.4`
- `jackson-databind`

### 2.2 Configuration application.yml

Section Thymeleaf minimale :

```yaml
spring:
  thymeleaf:
    cache: false          # mettre true en production
    prefix: classpath:/templates/
    suffix: .html
```

La configuration de securite est geree en Java (voir section 2.4).

### 2.3 Structure des fichiers

```
src/main/resources/
  templates/
    layout/
      base.html                  <-- Layout principal (topbar + sidebar + contenu)
      fragments/
        sidebar.html             <-- Navigation laterale
        topbar.html              <-- Barre superieure avec user menu
        flash.html               <-- Messages de succes/erreur
    auth/
      login.html                 <-- Page de connexion (standalone, pas de layout)
    dashboard.html               <-- Page d'accueil
    mon-module/
      list.html                  <-- Pages du module
      detail.html
  static/
    css/
      portal.css                 <-- CSS complementaire (scrollbars, animations)
    js/
      portal.js                  <-- Helpers JS (toast, SSE, fetch, confirm)
```

### 2.4 SecurityConfig.java -- Pattern complet

```java
package eu.lmvi.monprojet.config;

import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.config.annotation.web.configuration.EnableWebSecurity;
import org.springframework.security.crypto.bcrypt.BCryptPasswordEncoder;
import org.springframework.security.crypto.password.PasswordEncoder;
import org.springframework.security.web.SecurityFilterChain;
import org.springframework.security.web.util.matcher.AntPathRequestMatcher;

@Configuration
@EnableWebSecurity
public class SecurityConfig {

    @Bean
    public SecurityFilterChain filterChain(HttpSecurity http) throws Exception {
        http
            .csrf(csrf -> csrf
                .ignoringRequestMatchers(
                    new AntPathRequestMatcher("/api/**"),    // API REST sans CSRF
                    new AntPathRequestMatcher("/mcp/**"),    // MCP sans CSRF
                    new AntPathRequestMatcher("/admin/**"),  // Admin Socle sans CSRF
                    new AntPathRequestMatcher("/hub/login")  // Login POST
                )
            )
            .authorizeHttpRequests(auth -> auth
                .requestMatchers("/api/**").permitAll()      // API REST publique
                .requestMatchers("/admin/**").permitAll()    // Admin Socle
                .requestMatchers("/css/**", "/js/**", "/img/**", "/favicon.ico").permitAll()
                .requestMatchers("/hub/**").authenticated()  // Portail protege
                .anyRequest().permitAll()
            )
            .formLogin(form -> form
                .loginPage("/hub/login")           // Page de login custom
                .loginProcessingUrl("/hub/login")  // POST du formulaire
                .usernameParameter("email")        // Champ email (pas username)
                .defaultSuccessUrl("/hub", true)   // Redirect apres login
                .permitAll()
            )
            .logout(logout -> logout
                .logoutUrl("/hub/logout")
                .logoutSuccessUrl("/hub/login?logout")
                .permitAll()
            );

        return http.build();
    }

    @Bean
    public PasswordEncoder passwordEncoder() {
        return new BCryptPasswordEncoder();
    }
}
```

**Points importants :**

- Le CSRF est actif pour toutes les routes `/hub/**` **sauf** le login.
- Le CSRF est desactive pour `/api/**` (API REST) et `/mcp/**` (MCP protocol).
- Le formulaire login utilise `email` comme parametre (pas `username`).
- Les ressources statiques (`/css/**`, `/js/**`) sont en acces libre.

---

## 3. Layout System -- Code complet

### 3.1 base.html (COPIER-COLLER)

C'est le layout principal. Toutes les pages en heritent via `layout:decorate`.

```html
<!DOCTYPE html>
<html xmlns:th="http://www.thymeleaf.org"
      xmlns:layout="http://www.ultraq.net.nz/thymeleaf/layout"
      lang="fr">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">

    <!-- Titre dynamique : chaque page definit pageTitle dans le controller -->
    <title th:text="${pageTitle} + ' &mdash; SocleHub'">SocleHub</title>

    <!-- Tailwind CSS CDN (dev mode, pas de build) -->
    <script src="https://cdn.tailwindcss.com"></script>

    <!-- Alpine.js CDN (defer = charge apres le DOM) -->
    <script defer src="https://cdn.jsdelivr.net/npm/alpinejs@3.x.x/dist/cdn.min.js"></script>
</head>
<body class="bg-gray-50 font-sans text-gray-800"
      x-data="{ sidebarOpen: true }">

    <!-- Topbar fixe en haut -->
    <div th:replace="~{layout/fragments/topbar :: topbar}"></div>

    <!-- Container principal : sidebar + main -->
    <div class="flex h-screen pt-14">

        <!-- Sidebar fixe a gauche -->
        <aside th:replace="~{layout/fragments/sidebar :: sidebar(activeMenu=${activeMenu})}"></aside>

        <!-- Zone de contenu principale (scrollable) -->
        <main class="flex-1 overflow-y-auto p-6 ml-64">

            <!-- Messages flash (succes/erreur) -->
            <div th:replace="~{layout/fragments/flash :: flash}"></div>

            <!-- Contenu de la page (remplace par chaque template enfant) -->
            <div layout:fragment="content"></div>

        </main>
    </div>

</body>
</html>
```

**Architecture du layout :**

| Element | Classe Tailwind | Comportement |
|---------|----------------|--------------|
| `body` | `bg-gray-50 font-sans text-gray-800` | Fond gris clair, police systeme |
| Topbar | `fixed top-0 ... h-14` | Fixe, 56px de haut, z-30 |
| Container | `flex h-screen pt-14` | Flex horizontal, padding-top pour la topbar |
| Sidebar | `fixed left-0 top-14 ... w-64` | Fixe, 256px de large, z-10 |
| Main | `flex-1 overflow-y-auto p-6 ml-64` | Prend le reste, scroll vertical, marge gauche pour sidebar |

### 3.2 topbar.html (COPIER-COLLER)

Barre superieure avec logo et menu utilisateur.

```html
<!DOCTYPE html>
<html xmlns:th="http://www.thymeleaf.org" lang="fr">
<body>

<header th:fragment="topbar"
        class="fixed top-0 left-0 right-0 h-14 bg-white border-b border-gray-200 z-30
               flex items-center justify-between px-6">

    <!-- Gauche : Logo + Titre -->
    <div class="flex items-center gap-3">
        <svg class="w-7 h-7 text-indigo-600" viewBox="0 0 24 24" fill="none" stroke="currentColor"
             stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 2L2 7l10 5 10-5-10-5z"/>
            <path d="M2 17l10 5 10-5"/>
            <path d="M2 12l10 5 10-5"/>
        </svg>
        <a th:href="@{/hub}" class="text-lg font-bold text-gray-900 hover:text-indigo-600 transition-colors">
            SocleHub
        </a>
    </div>

    <!-- Droite : Menu utilisateur avec dropdown Alpine.js -->
    <div class="flex items-center gap-4" x-data="{ userMenuOpen: false }">

        <div class="relative">
            <button @click="userMenuOpen = !userMenuOpen"
                    class="flex items-center gap-2 text-sm text-gray-700 hover:text-gray-900
                           focus:outline-none">
                <!-- Avatar avec initiales -->
                <div class="w-8 h-8 rounded-full bg-indigo-100 text-indigo-600 flex items-center
                            justify-center text-xs font-bold"
                     th:text="${#strings.toUpperCase(#strings.substring(currentUser?.firstName ?: 'A', 0, 1)) +
                               #strings.toUpperCase(#strings.substring(currentUser?.lastName ?: 'D', 0, 1))}">
                    AD
                </div>
                <span class="hidden sm:inline"
                      th:text="${currentUser?.firstName ?: 'Admin'}">Admin</span>
                <!-- Chevron -->
                <svg class="w-4 h-4 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"/>
                </svg>
            </button>

            <!-- Dropdown avec transitions Alpine.js -->
            <div x-show="userMenuOpen"
                 @click.outside="userMenuOpen = false"
                 x-transition:enter="transition ease-out duration-100"
                 x-transition:enter-start="transform opacity-0 scale-95"
                 x-transition:enter-end="transform opacity-100 scale-100"
                 x-transition:leave="transition ease-in duration-75"
                 x-transition:leave-start="transform opacity-100 scale-100"
                 x-transition:leave-end="transform opacity-0 scale-95"
                 class="absolute right-0 mt-2 w-48 bg-white rounded-lg shadow-lg
                        border border-gray-200 z-50 py-1">

                <div class="px-4 py-2 border-b border-gray-100">
                    <p class="text-sm font-medium text-gray-900"
                       th:text="${currentUser?.firstName ?: 'Admin'} + ' ' + ${currentUser?.lastName ?: ''}">
                        Admin
                    </p>
                    <p class="text-xs text-gray-500"
                       th:text="${currentUser?.email ?: 'admin@soclehub.local'}">
                        admin@soclehub.local
                    </p>
                </div>

                <a th:href="@{/hub/iam}"
                   class="block px-4 py-2 text-sm text-gray-700 hover:bg-gray-50">
                    Utilisateurs
                </a>

                <div class="border-t border-gray-100 my-1"></div>

                <!-- Logout avec token CSRF -->
                <form th:action="@{/hub/logout}" method="post">
                    <input type="hidden" th:name="${_csrf?.parameterName}" th:value="${_csrf?.token}"/>
                    <button type="submit"
                            class="w-full text-left px-4 py-2 text-sm text-red-600 hover:bg-red-50">
                        Se deconnecter
                    </button>
                </form>
            </div>
        </div>

    </div>

</header>

</body>
</html>
```

**Pour adapter a votre projet :**
- Remplacer le titre "SocleHub" par le nom de votre application.
- Remplacer le SVG du logo par le votre.
- Adapter les liens du dropdown (ex: profil, parametres).
- L'objet `currentUser` est injecte dans le modele Thymeleaf par un `@ControllerAdvice` ou dans chaque controller.

### 3.3 sidebar.html -- Structure et pattern

```html
<!DOCTYPE html>
<html xmlns:th="http://www.thymeleaf.org" lang="fr">
<body>

<aside th:fragment="sidebar(activeMenu)"
       class="fixed left-0 top-14 h-full w-64 bg-white border-r border-gray-200 z-10 overflow-y-auto">
    <nav class="p-4 space-y-1">

        <!-- ========================================= -->
        <!-- LIEN SIMPLE (pattern Dashboard)           -->
        <!-- ========================================= -->
        <a th:href="@{/hub}"
           th:classappend="${activeMenu == 'dashboard'} ? 'bg-indigo-50 text-indigo-700' : 'text-gray-600 hover:bg-gray-50'"
           class="flex items-center gap-3 px-3 py-2 rounded-md text-sm font-medium">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"
                 stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                <path d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-4 0a1 1 0 01-1-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 01-1 1h-2z"/>
            </svg>
            <span>Dashboard</span>
        </a>

        <!-- ========================================= -->
        <!-- LIEN SIMPLE (pattern Applications)        -->
        <!-- ========================================= -->
        <a th:href="@{/hub/apps}"
           th:classappend="${activeMenu == 'apps'} ? 'bg-indigo-50 text-indigo-700' : 'text-gray-600 hover:bg-gray-50'"
           class="flex items-center gap-3 px-3 py-2 rounded-md text-sm font-medium">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"
                 stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                <path d="M20 7l-8-4-8 4m16 0l-8 4m8-4v10l-8 4m0-10L4 7m8 4v10M4 7v10l8 4"/>
            </svg>
            <span>Applications</span>
        </a>

        <!-- ========================================= -->
        <!-- SOUS-MENU DEPLIABLE (pattern IAM)         -->
        <!-- ========================================= -->
        <div x-data="{ iamOpen: ${activeMenu == 'users' || activeMenu == 'roles' || activeMenu == 'scopes' || activeMenu == 'api-keys'} }">
            <button @click="iamOpen = !iamOpen"
                    class="w-full flex items-center justify-between gap-3 px-3 py-2 rounded-md text-sm font-medium"
                    th:classappend="${activeMenu == 'users' || activeMenu == 'roles' || activeMenu == 'scopes' || activeMenu == 'api-keys'} ? 'text-indigo-700' : 'text-gray-600 hover:bg-gray-50'">
                <div class="flex items-center gap-3">
                    <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"
                         stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                        <path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2"/>
                        <circle cx="9" cy="7" r="4"/>
                        <path d="M23 21v-2a4 4 0 00-3-3.87"/>
                        <path d="M16 3.13a4 4 0 010 7.75"/>
                    </svg>
                    <span>IAM</span>
                </div>
                <!-- Chevron qui tourne -->
                <svg :class="iamOpen && 'rotate-90'" class="w-4 h-4 transition-transform duration-200" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7"/>
                </svg>
            </button>
            <!-- Sous-liens (x-show + x-collapse) -->
            <div x-show="iamOpen" x-collapse class="ml-6 space-y-0.5 mt-1">
                <a th:href="@{/hub/iam/users}"
                   th:classappend="${activeMenu == 'users'} ? 'bg-indigo-50 text-indigo-700' : 'text-gray-500 hover:bg-gray-50'"
                   class="block px-3 py-1.5 rounded-md text-xs font-medium">
                    Utilisateurs
                </a>
                <a th:href="@{/hub/iam/roles}"
                   th:classappend="${activeMenu == 'roles'} ? 'bg-indigo-50 text-indigo-700' : 'text-gray-500 hover:bg-gray-50'"
                   class="block px-3 py-1.5 rounded-md text-xs font-medium">
                    Roles
                </a>
                <a th:href="@{/hub/iam/scopes}"
                   th:classappend="${activeMenu == 'scopes'} ? 'bg-indigo-50 text-indigo-700' : 'text-gray-500 hover:bg-gray-50'"
                   class="block px-3 py-1.5 rounded-md text-xs font-medium">
                    Scopes
                </a>
            </div>
        </div>

        <!-- ========================================= -->
        <!-- SEPARATEUR                                -->
        <!-- ========================================= -->
        <div class="border-t border-gray-200 pt-2 mt-2"></div>

        <!-- Autres liens apres le separateur... -->

    </nav>
</aside>

</body>
</html>
```

**Gestion de l'etat actif :**
- Chaque page definit `model.addAttribute("activeMenu", "dashboard")` dans le controller.
- Le `th:classappend` compare `activeMenu` et applique `bg-indigo-50 text-indigo-700` si actif.
- Pour les sous-menus, `x-data` initialise l'ouverture si un des sous-elements est actif.

### 3.4 flash.html (COPIER-COLLER)

Messages flash avec auto-dismiss Alpine.js.

```html
<!DOCTYPE html>
<html xmlns:th="http://www.thymeleaf.org" lang="fr">
<body>

<div th:fragment="flash">

    <!-- Message de succes -->
    <div th:if="${success}"
         x-data="{ show: true }"
         x-init="setTimeout(() => show = false, 4000)"
         x-show="show"
         x-transition:enter="transition ease-out duration-300"
         x-transition:enter-start="opacity-0 transform -translate-y-2"
         x-transition:enter-end="opacity-100 transform translate-y-0"
         x-transition:leave="transition ease-in duration-200"
         x-transition:leave-start="opacity-100"
         x-transition:leave-end="opacity-0"
         class="mb-4 p-4 bg-green-50 border border-green-200 rounded-lg text-green-800
                flex items-center justify-between gap-2">
        <div class="flex items-center gap-2">
            <svg class="w-5 h-5 text-green-500 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
                      d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z"/>
            </svg>
            <span th:text="${success}" class="text-sm"></span>
        </div>
        <button @click="show = false" class="text-green-500 hover:text-green-700">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"/>
            </svg>
        </button>
    </div>

    <!-- Message d'erreur -->
    <div th:if="${error}"
         x-data="{ show: true }"
         x-init="setTimeout(() => show = false, 4000)"
         x-show="show"
         x-transition:enter="transition ease-out duration-300"
         x-transition:enter-start="opacity-0 transform -translate-y-2"
         x-transition:enter-end="opacity-100 transform translate-y-0"
         x-transition:leave="transition ease-in duration-200"
         x-transition:leave-start="opacity-100"
         x-transition:leave-end="opacity-0"
         class="mb-4 p-4 bg-red-50 border border-red-200 rounded-lg text-red-800
                flex items-center justify-between gap-2">
        <div class="flex items-center gap-2">
            <svg class="w-5 h-5 text-red-500 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
                      d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"/>
            </svg>
            <span th:text="${error}" class="text-sm"></span>
        </div>
        <button @click="show = false" class="text-red-500 hover:text-red-700">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"/>
            </svg>
        </button>
    </div>

</div>

</body>
</html>
```

**Utilisation dans le controller :**

```java
redirectAttributes.addFlashAttribute("success", "Operation reussie.");
redirectAttributes.addFlashAttribute("error", "Erreur : " + result.error());
```

Les flash attributes sont transmis via `RedirectAttributes` lors d'un `redirect:`.

---

## 4. Palette de couleurs

Toutes les classes Tailwind utilisees et leur signification semantique :

| Usage | Classes Tailwind | Contexte |
|-------|-----------------|----------|
| Fond de page | `bg-gray-50` | `body` |
| Carte / panneau | `bg-white rounded-xl border border-gray-200 shadow-sm` | Conteneur principal |
| Carte avec padding | `bg-white rounded-xl border border-gray-200 shadow-sm p-6` | Section de contenu |
| Carte de danger | `bg-white rounded-xl border border-red-200 shadow-sm p-6` | Zone danger |
| Topbar | `bg-white border-b border-gray-200` | Barre superieure |
| Sidebar | `bg-white border-r border-gray-200` | Navigation laterale |
| Titre H1 | `text-2xl font-bold text-gray-900` | Titre de page |
| Titre H2 (carte) | `text-base font-semibold text-gray-900` | Titre de section |
| Titre danger | `text-base font-semibold text-red-700` | Zone danger |
| Texte principal | `text-gray-900` | Noms, valeurs |
| Texte secondaire | `text-gray-600` | Descriptions |
| Texte tertiaire | `text-gray-500` | Labels, sous-titres |
| Texte discret | `text-gray-400` | Hints, placeholders |
| Lien | `text-indigo-600 hover:text-indigo-800 hover:underline` | Liens cliquables |
| Bouton primaire | `bg-indigo-600 text-white hover:bg-indigo-700` | Action principale |
| Bouton secondaire | `border border-gray-300 text-gray-700 hover:bg-gray-50` | Action secondaire |
| Bouton danger | `bg-red-600 text-white hover:bg-red-700` | Suppression |
| Bouton outline danger | `text-red-600 border border-red-200 hover:bg-red-50` | Revoquer, retirer |
| Bouton vert | `bg-green-600 text-white hover:bg-green-700` | Creer (variante DNS) |
| Badge actif/vert | `bg-green-100 text-green-700` + dot `bg-green-500` | Statut actif |
| Badge inactif/gris | `bg-gray-100 text-gray-600` + dot `bg-gray-400` | Statut arrete |
| Badge erreur/rouge | `bg-red-100 text-red-700` + dot `bg-red-500` | Statut erreur |
| Badge installation | `bg-indigo-100 text-indigo-700` + dot `bg-indigo-500 animate-pulse` | En cours |
| Badge warning | `bg-amber-100 text-amber-700` + dot `bg-amber-500` | Attention |
| Badge proxy orange | `bg-orange-100 text-orange-700` | Proxy Cloudflare |
| Badge role | `bg-indigo-100 text-indigo-700 rounded text-xs` | Nom de role |
| Badge scope | `bg-emerald-50 text-emerald-700 border border-emerald-200 rounded-full` | Scope effectif |
| Badge scope mono | `bg-emerald-100 text-emerald-700 rounded text-xs font-mono` | Nom de scope |
| Badge tenant | `bg-gray-100 text-gray-600 rounded text-xs` | Tenant ID |
| Badge type DNS A | `bg-blue-100 text-blue-700` | Type A/AAAA |
| Badge type CNAME | `bg-purple-100 text-purple-700` | Type CNAME |
| Badge type TXT | `bg-amber-100 text-amber-700` | Type TXT |
| Badge type MX | `bg-green-100 text-green-700` | Type MX |
| Fond tableau head | `bg-gray-50 border-b border-gray-200` | Entete de tableau |
| Ligne tableau | `hover:bg-gray-50` | Lignes de tableau |
| Separateur | `divide-y divide-gray-100` | Entre les lignes |
| Fond formulaire inline | `bg-gray-50 rounded-lg px-4 py-2.5` | Ligne de role avec action |
| Input focus | `focus:outline-none focus:border-indigo-500` | Champs de saisie |
| Sidebar actif | `bg-indigo-50 text-indigo-700` | Lien actif |
| Sidebar inactif | `text-gray-600 hover:bg-gray-50` | Lien inactif |
| Sidebar sous-lien actif | `bg-indigo-50 text-indigo-700` | Sous-lien actif |
| Sidebar sous-lien inactif | `text-gray-500 hover:bg-gray-50` | Sous-lien inactif |
| Flash succes | `bg-green-50 border border-green-200 text-green-800` | Message succes |
| Flash erreur | `bg-red-50 border border-red-200 text-red-800` | Message erreur |
| Alerte info | `bg-blue-50 border-blue-200 text-blue-800` | Alerte informative |
| Alerte warning | `bg-amber-50 border-amber-200 text-amber-800` | Alerte avertissement |
| Alerte danger | `bg-red-50 border-red-200 text-red-800` | Alerte critique |
| Terminal fond | `bg-gray-900` | Zone de logs |
| Terminal header | `bg-gray-800 border-b border-gray-700` | Barre terminal |
| Terminal texte | `text-gray-200 font-mono text-xs` | Contenu logs |
| Log ERROR | `text-red-400` / label `text-red-500` | Ligne d'erreur |
| Log WARN | `text-amber-400` / label `text-amber-500` | Ligne warning |
| Log INFO | `text-cyan-300` / label `text-cyan-400` | Ligne info |
| Log DEBUG | `text-gray-500` / label `text-gray-600` | Ligne debug |
| Modal backdrop | `bg-black/50` | Fond modal |
| Modal carte | `bg-white rounded-xl shadow-xl p-6` | Contenu modal |
| Login carte | `bg-white rounded-2xl border border-gray-200 shadow-lg p-10 w-96` | Formulaire login |

---

## 5. Composants -- Snippets prets a l'emploi

### 5.1 Carte standard

Une carte blanche avec titre et contenu.

```html
<div class="bg-white rounded-xl border border-gray-200 shadow-sm p-6">
    <h2 class="text-base font-semibold text-gray-900 mb-4">Titre de la section</h2>
    <!-- Contenu ici -->
</div>
```

### 5.2 Stat Card (Dashboard)

Carte de statistique avec indicateur de couleur en bas.

```html
<div class="bg-white rounded-xl border border-gray-200 shadow-sm p-4">
    <p class="text-sm text-gray-500 font-medium">Applications</p>
    <p class="text-3xl font-bold text-gray-900 mt-1"
       th:text="${stats.appsRunning}">0</p>
    <p class="text-sm text-gray-500 mt-1"
       th:text="${stats.appsStopped} + ' arretees - ' + ${stats.appsError} + ' en erreur'"></p>
    <div class="mt-2 h-1 rounded"
         th:classappend="${stats.appsError > 0} ? 'bg-red-400' : 'bg-green-400'"></div>
</div>
```

**Grille de stats (4 colonnes) :**

```html
<div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
    <!-- Stat cards ici -->
</div>
```

**Code controller :**

```java
model.addAttribute("stats", new DashboardStats(
    appsRunning, appsStopped, appsError,
    certsValid, certsExpiringSoon,
    dnsRecords, dnsDrifts,
    services
));
```

### 5.3 Table de donnees avec actions

Pattern complet d'un tableau dans une carte.

```html
<div class="bg-white rounded-xl border border-gray-200 shadow-sm overflow-hidden">
    <table class="w-full text-sm">
        <thead class="bg-gray-50 border-b border-gray-200">
            <tr>
                <th class="px-4 py-3 text-left font-medium text-gray-500">Email</th>
                <th class="px-4 py-3 text-left font-medium text-gray-500">Nom</th>
                <th class="px-4 py-3 text-left font-medium text-gray-500">Statut</th>
                <th class="px-4 py-3 text-right font-medium text-gray-500">Actions</th>
            </tr>
        </thead>
        <tbody class="divide-y divide-gray-100">
            <tr th:each="item : ${items}" class="hover:bg-gray-50">

                <!-- Colonne lien -->
                <td class="px-4 py-3">
                    <a th:href="@{/hub/mon-module/{id}(id=${item['id']})}"
                       class="font-medium text-indigo-600 hover:text-indigo-800 hover:underline"
                       th:text="${item['email']}"></a>
                </td>

                <!-- Colonne texte -->
                <td class="px-4 py-3 text-gray-700"
                    th:text="${item['name']}"></td>

                <!-- Colonne badge -->
                <td class="px-4 py-3">
                    <span th:if="${!item['locked']}"
                          class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs
                                 font-medium bg-green-100 text-green-700">
                        <span class="w-1.5 h-1.5 rounded-full bg-green-500"></span>Actif
                    </span>
                    <span th:if="${item['locked']}"
                          class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs
                                 font-medium bg-gray-100 text-gray-600">
                        <span class="w-1.5 h-1.5 rounded-full bg-gray-400"></span>Verrouille
                    </span>
                </td>

                <!-- Colonne actions -->
                <td class="px-4 py-3 text-right">
                    <div class="flex items-center justify-end gap-1">
                        <!-- Detail -->
                        <a th:href="@{/hub/mon-module/{id}(id=${item['id']})}"
                           class="p-1.5 text-gray-400 hover:text-indigo-600 rounded hover:bg-indigo-50
                                  transition-colors"
                           title="Details">
                            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
                                      d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"/>
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
                                      d="M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z"/>
                            </svg>
                        </a>

                        <!-- Supprimer (avec confirm) -->
                        <form th:action="@{/hub/mon-module/{id}/delete(id=${item['id']})}"
                              method="post" class="inline"
                              x-data
                              @submit.prevent="if(confirm('Supprimer cet element ?')) $el.submit()">
                            <button type="submit"
                                    class="p-1.5 text-gray-400 hover:text-red-600 rounded hover:bg-red-50
                                           transition-colors"
                                    title="Supprimer">
                                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
                                          d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"/>
                                </svg>
                            </button>
                        </form>
                    </div>
                </td>
            </tr>
        </tbody>
    </table>
</div>
```

### 5.4 Badges de statut (6 variantes)

```html
<!-- Actif / En ligne / Valide -->
<span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-green-100 text-green-700">
    <span class="w-1.5 h-1.5 rounded-full bg-green-500"></span>Actif
</span>

<!-- Arrete / Inactif -->
<span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-gray-100 text-gray-600">
    <span class="w-1.5 h-1.5 rounded-full bg-gray-400"></span>Arrete
</span>

<!-- Erreur / Expire -->
<span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-red-100 text-red-700">
    <span class="w-1.5 h-1.5 rounded-full bg-red-500"></span>Erreur
</span>

<!-- En cours / Installation (dot pulse) -->
<span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-indigo-100 text-indigo-700">
    <span class="w-1.5 h-1.5 rounded-full bg-indigo-500 animate-pulse"></span>Installation...
</span>

<!-- Warning / A renouveler -->
<span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-amber-100 text-amber-700">
    <span class="w-1.5 h-1.5 rounded-full bg-amber-500"></span>A renouveler
</span>

<!-- Revoque -->
<span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-red-100 text-red-700">
    <span class="w-1.5 h-1.5 rounded-full bg-red-500"></span>Revoquee
</span>
```

**Pattern avec `th:switch` :**

```html
<span th:switch="${item.status}">
    <span th:case="'running'" class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-green-100 text-green-700">
        <span class="w-1.5 h-1.5 rounded-full bg-green-500"></span>En ligne
    </span>
    <span th:case="'stopped'" class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-gray-100 text-gray-600">
        <span class="w-1.5 h-1.5 rounded-full bg-gray-400"></span>Arrete
    </span>
    <span th:case="'error'" class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-red-100 text-red-700">
        <span class="w-1.5 h-1.5 rounded-full bg-red-500"></span>Erreur
    </span>
</span>
```

### 5.5 Boutons (5 variantes)

```html
<!-- Primaire (action principale) -->
<button class="px-4 py-2 bg-indigo-600 text-white rounded-lg text-sm font-medium
               hover:bg-indigo-700 transition-colors">
    Creer
</button>

<!-- Secondaire (action secondaire) -->
<button class="px-3 py-1.5 text-sm border border-gray-300 text-gray-700 rounded-lg
               hover:bg-gray-50 transition-colors">
    Annuler
</button>

<!-- Danger (suppression) -->
<button class="px-3 py-1.5 text-sm bg-red-600 text-white rounded-lg
               hover:bg-red-700 font-medium">
    Supprimer
</button>

<!-- Petit bouton d'action (dans un tableau) -->
<button class="px-2.5 py-1 text-xs text-red-600 border border-red-200 rounded
               hover:bg-red-50 transition-colors">
    Revoquer
</button>

<!-- Bouton icone (action dans un tableau) -->
<button class="p-1.5 text-gray-400 hover:text-red-600 rounded hover:bg-red-50
               transition-colors" title="Supprimer">
    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
              d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"/>
    </svg>
</button>
```

### 5.6 Formulaire dans une carte

Pattern formulaire horizontal avec grille.

```html
<div class="bg-white rounded-xl border border-gray-200 shadow-sm p-6">
    <h2 class="text-base font-semibold text-gray-900 mb-4">Creer un element</h2>

    <form th:action="@{/hub/mon-module}" method="post">

        <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <div>
                <label class="block text-sm font-medium text-gray-700 mb-1">Nom *</label>
                <input type="text" name="name" required
                       class="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm
                              focus:outline-none focus:border-indigo-500"/>
            </div>
            <div>
                <label class="block text-sm font-medium text-gray-700 mb-1">Description</label>
                <input type="text" name="description"
                       class="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm
                              focus:outline-none focus:border-indigo-500"/>
            </div>
        </div>

        <div class="flex justify-end mt-4">
            <button type="submit"
                    class="px-6 py-2 bg-indigo-600 text-white rounded-lg text-sm font-medium
                           hover:bg-indigo-700 transition-colors">
                Creer
            </button>
        </div>

    </form>
</div>
```

### 5.7 Formulaire collapsible (toggle Alpine.js)

Pattern utilise sur les pages de liste pour afficher/masquer le formulaire de creation.

```html
<div layout:fragment="content"
     x-data="{ showCreate: false }">

    <!-- Header avec bouton toggle -->
    <div class="flex justify-between items-center mb-6">
        <h1 class="text-2xl font-bold text-gray-900">Mes elements</h1>
        <button @click="showCreate = !showCreate"
                class="px-4 py-2 bg-indigo-600 text-white rounded-lg text-sm font-medium
                       hover:bg-indigo-700 transition-colors flex items-center gap-2">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4"/>
            </svg>
            <span x-text="showCreate ? 'Annuler' : 'Creer un element'"></span>
        </button>
    </div>

    <!-- Formulaire collapsible -->
    <div x-show="showCreate"
         x-transition:enter="transition ease-out duration-200"
         x-transition:enter-start="opacity-0 -translate-y-2"
         x-transition:enter-end="opacity-100 translate-y-0"
         x-transition:leave="transition ease-in duration-150"
         x-transition:leave-start="opacity-100 translate-y-0"
         x-transition:leave-end="opacity-0 -translate-y-2"
         class="bg-white rounded-xl border border-gray-200 shadow-sm p-6 mb-6">

        <div class="flex items-center justify-between mb-4">
            <h2 class="text-base font-semibold text-gray-900">Nouvel element</h2>
            <button @click="showCreate = false" class="text-gray-400 hover:text-gray-600">
                <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"/>
                </svg>
            </button>
        </div>

        <form th:action="@{/hub/mon-module}" method="post">
            <!-- Champs du formulaire -->
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
                <!-- ... -->
            </div>
            <div class="flex justify-end mt-4">
                <button type="submit"
                        class="px-6 py-2 bg-indigo-600 text-white rounded-lg text-sm font-medium
                               hover:bg-indigo-700 transition-colors">
                    Creer
                </button>
            </div>
        </form>
    </div>

    <!-- Table de donnees en dessous -->
    <!-- ... -->

</div>
```

### 5.8 Modale de confirmation (delete pattern)

```html
<div layout:fragment="content"
     x-data="{ confirmDelete: false }">

    <!-- Bouton declencheur -->
    <button @click="confirmDelete = true"
            class="px-3 py-1.5 text-sm bg-red-600 text-white rounded-lg hover:bg-red-700">
        Supprimer
    </button>

    <!-- Modale -->
    <div x-show="confirmDelete" x-transition
         class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
         @keydown.escape.window="confirmDelete = false">
        <div class="bg-white rounded-xl shadow-xl p-6 max-w-md w-full mx-4"
             @click.outside="confirmDelete = false">
            <h3 class="text-lg font-semibold text-gray-900 mb-2">Confirmer la suppression</h3>
            <p class="text-sm text-gray-600 mb-6">
                Supprimer cet element ? Cette action est irreversible.
            </p>
            <div class="flex justify-end gap-3">
                <button @click="confirmDelete = false"
                        class="px-4 py-2 text-sm text-gray-700 border border-gray-300 rounded-lg hover:bg-gray-50">
                    Annuler
                </button>
                <form th:action="@{/hub/mon-module/{id}/delete(id=${item['id']})}"
                      method="post" class="inline">
                    <button type="submit"
                            class="px-4 py-2 text-sm bg-red-600 text-white rounded-lg hover:bg-red-700 font-medium">
                        Supprimer definitivement
                    </button>
                </form>
            </div>
        </div>
    </div>

</div>
```

### 5.9 Breadcrumb

```html
<div class="flex items-center gap-2 text-sm text-gray-500 mb-2">
    <a th:href="@{/hub/mon-module}" class="hover:text-indigo-600">Mon module</a>
    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7"/>
    </svg>
    <span th:text="${item['name']}" class="text-gray-900"></span>
</div>
```

### 5.10 Page header avec bouton action

```html
<div class="flex justify-between items-center mb-6">
    <h1 class="text-2xl font-bold text-gray-900">Titre de la page</h1>
    <a th:href="@{/hub/mon-module/create}"
       class="bg-indigo-600 text-white px-4 py-2 rounded-lg text-sm font-medium
              hover:bg-indigo-700 transition-colors">
        + Creer
    </a>
</div>
```

**Variante avec badge de statut :**

```html
<div class="flex items-center justify-between">
    <div class="flex items-center gap-3">
        <h1 class="text-2xl font-bold text-gray-900" th:text="${item['name']}"></h1>
        <span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-green-100 text-green-700">
            <span class="w-1.5 h-1.5 rounded-full bg-green-500"></span>Actif
        </span>
    </div>
    <div class="flex items-center gap-2">
        <!-- Boutons d'action -->
    </div>
</div>
```

### 5.11 Section formulaire creation (dans une carte)

Pattern pour un formulaire inline en bas d'une section (ex: assigner un role).

```html
<div class="border-t border-gray-200 pt-4">
    <h3 class="text-sm font-medium text-gray-700 mb-3">Assigner un role</h3>
    <form th:action="@{/hub/iam/users/{id}/roles(id=${user['user_id']})}" method="post"
          class="flex items-end gap-3">
        <div class="flex-1">
            <select name="roleName" required
                    class="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm
                           focus:outline-none focus:border-indigo-500">
                <option value="">-- Choisir un role --</option>
                <option th:each="r : ${allRoles}" th:value="${r}" th:text="${r}"></option>
            </select>
        </div>
        <button type="submit"
                class="px-4 py-2 bg-indigo-600 text-white rounded-lg text-sm font-medium
                       hover:bg-indigo-700">
            Assigner
        </button>
    </form>
</div>
```

### 5.12 Section liste avec badges (roles, scopes)

```html
<div class="bg-white rounded-xl border border-gray-200 shadow-sm p-6">
    <h2 class="text-base font-semibold text-gray-900 mb-4">Roles</h2>

    <div th:if="${user['roles'] != null}" class="space-y-2 mb-6">
        <div th:each="role : ${user['roles']}"
             class="flex items-center justify-between bg-gray-50 rounded-lg px-4 py-2.5">
            <span class="px-2 py-0.5 bg-indigo-100 text-indigo-700 rounded text-xs font-medium"
                  th:text="${role}"></span>
            <form th:action="@{/hub/iam/users/{id}/roles/revoke(id=${user['user_id']})}" method="post">
                <input type="hidden" name="roleName" th:value="${role}"/>
                <button type="submit"
                        class="px-2.5 py-1 text-xs text-red-600 border border-red-200 rounded hover:bg-red-50">
                    Revoquer
                </button>
            </form>
        </div>
    </div>
</div>
```

**Scopes en pills :**

```html
<div class="flex flex-wrap gap-2">
    <span th:each="scope : ${user['scopes']}"
          class="px-2.5 py-1 bg-emerald-50 text-emerald-700 border border-emerald-200 rounded-full text-xs font-medium"
          th:text="${scope}"></span>
</div>
```

### 5.13 Tableau de cles API avec flash raw key

Affichage unique de la cle creee :

```html
<!-- Flash: cle API creee (visible une seule fois apres creation) -->
<div th:if="${rawKey}"
     x-data="{ copied: false }"
     class="mb-6 p-4 bg-green-50 border border-green-200 rounded-xl">
    <div class="flex items-start gap-3">
        <svg class="w-5 h-5 text-green-500 flex-shrink-0 mt-0.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
                  d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z"/>
        </svg>
        <div class="flex-1">
            <h3 class="text-sm font-semibold text-green-800 mb-1">Cle API creee avec succes</h3>
            <p class="text-xs text-green-700 mb-3">
                Copiez cette cle maintenant. Elle ne sera plus affichee apres cette page.
            </p>
            <div class="flex items-center gap-2">
                <code class="flex-1 bg-white border border-green-300 rounded-lg px-3 py-2 text-sm
                             font-mono text-gray-900 select-all break-all"
                      th:text="${rawKey}"
                      x-ref="keyValue"></code>
                <button @click="navigator.clipboard.writeText($refs.keyValue.textContent); copied = true; setTimeout(() => copied = false, 2000)"
                        class="px-3 py-2 text-sm border border-green-300 rounded-lg
                               hover:bg-green-100 transition-colors flex-shrink-0"
                        :class="copied ? 'bg-green-100 text-green-700' : 'text-green-600'">
                    <span x-show="!copied">Copier</span>
                    <span x-show="copied">Copie !</span>
                </button>
            </div>
        </div>
    </div>
</div>
```

### 5.14 Dropdown/Select avec option saisie manuelle

Pattern utilise pour le formulaire de certificat TLS.

```html
<div class="flex-1" x-data="{ custom: false }">
    <label class="block text-sm font-medium text-gray-700 mb-1">Domaine *</label>
    <div class="flex items-center gap-2">
        <!-- Mode select -->
        <select x-show="!custom" name="domain" required
                class="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm
                       focus:outline-none focus:border-indigo-500">
            <option value="">-- Choisir un domaine --</option>
            <option th:each="d : ${domains}" th:value="${d}" th:text="${d}"></option>
        </select>
        <!-- Mode saisie manuelle -->
        <input x-show="custom" type="text" name="domain" placeholder="domaine.example.com"
               x-bind:required="custom"
               class="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm
                      focus:outline-none focus:border-indigo-500"/>
    </div>
    <!-- Toggle entre les deux modes -->
    <div class="flex items-center gap-2 mt-1">
        <button type="button" @click="custom = !custom" class="text-xs text-indigo-600 hover:underline">
            <span x-show="!custom">Saisir manuellement</span>
            <span x-show="custom">Choisir dans la liste</span>
        </button>
    </div>
</div>
```

### 5.15 Terminal / Zone de logs

Zone type terminal pour afficher des logs.

```html
<!-- Preview de logs (statique) -->
<div class="bg-gray-900 rounded-lg p-4 max-h-64 overflow-y-auto font-mono text-xs">
    <div th:if="${logs != null}" th:each="line : ${logs}"
         class="text-gray-200 leading-5">
        <span th:text="${line}"></span>
    </div>
    <p th:if="${logs == null or #lists.isEmpty(logs)}"
       class="text-gray-500">Aucun log disponible.</p>
</div>

<!-- Terminal complet avec barre de titre -->
<div class="bg-gray-900 rounded-xl overflow-hidden">
    <!-- Barre de titre type macOS -->
    <div class="bg-gray-800 px-4 py-2 flex items-center justify-between border-b border-gray-700">
        <div class="flex items-center gap-2">
            <span class="w-3 h-3 rounded-full bg-red-500"></span>
            <span class="w-3 h-3 rounded-full bg-amber-500"></span>
            <span class="w-3 h-3 rounded-full bg-green-500"></span>
            <span class="text-gray-400 text-xs ml-2 font-mono">logs://mon-app</span>
        </div>
    </div>
    <!-- Zone de contenu scrollable -->
    <div class="p-4 h-[55vh] overflow-y-auto font-mono text-xs">
        <!-- Lignes de log ici -->
    </div>
</div>
```

### 5.16 Etat vide (empty state)

```html
<div th:if="${#lists.isEmpty(items)}"
     class="text-center py-12 text-gray-500">
    <svg class="w-12 h-12 mx-auto text-gray-300 mb-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5"
              d="M20 7l-8-4-8 4m16 0l-8 4m8-4v10l-8 4m0-10L4 7m8 4v10M4 7v10l8 4"/>
    </svg>
    <p class="text-sm">Aucun element.</p>
    <button @click="showCreate = true"
            class="mt-2 text-sm text-indigo-600 hover:underline">
        Creer le premier element
    </button>
</div>
```

Ce composant se place a l'interieur de la carte du tableau, juste apres le `</table>`.

### 5.17 Alertes (info, warning, danger)

```html
<!-- Zone d'alertes dynamiques -->
<div th:if="${alerts != null and not #lists.isEmpty(alerts)}" class="mb-6 space-y-2">
    <div th:each="alert : ${alerts}"
         th:classappend="${alert['level'] == 'danger'} ? 'bg-red-50 border-red-200 text-red-800' :
                         (${alert['level'] == 'warning'}  ? 'bg-amber-50 border-amber-200 text-amber-800' :
                                                       'bg-blue-50 border-blue-200 text-blue-800')"
         class="flex items-center justify-between p-3 rounded-lg border text-sm">
        <span th:text="${alert['message']}"></span>
        <a th:if="${alert['actionUrl']}"
           th:href="${alert['actionUrl']}"
           class="ml-4 font-medium underline whitespace-nowrap"
           th:text="${alert['actionLabel']}"></a>
    </div>
</div>
```

**Code controller :**

```java
List<Map<String, String>> alerts = new ArrayList<>();
alerts.add(Map.of("level", "warning", "message", "3 certificats expirent bientot"));
alerts.add(Map.of("level", "danger", "message", "2 applications en erreur",
                   "actionUrl", "/hub/apps", "actionLabel", "Voir les apps"));
model.addAttribute("alerts", alerts);
```

### 5.18 Menu sidebar -- lien simple

Pattern pour ajouter un nouveau lien dans la sidebar :

```html
<a th:href="@{/hub/mon-module}"
   th:classappend="${activeMenu == 'mon-module'} ? 'bg-indigo-50 text-indigo-700' : 'text-gray-600 hover:bg-gray-50'"
   class="flex items-center gap-3 px-3 py-2 rounded-md text-sm font-medium">
    <!-- Icone SVG 24x24 -->
    <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"
         stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
        <!-- Vos paths SVG ici -->
    </svg>
    <span>Mon module</span>
</a>
```

### 5.19 Menu sidebar -- sous-menu depliable

Pattern pour un groupe de liens avec expansion Alpine.js :

```html
<div x-data="{ menuOpen: ${activeMenu == 'sub1' || activeMenu == 'sub2' || activeMenu == 'sub3'} }">
    <button @click="menuOpen = !menuOpen"
            class="w-full flex items-center justify-between gap-3 px-3 py-2 rounded-md text-sm font-medium"
            th:classappend="${activeMenu == 'sub1' || activeMenu == 'sub2' || activeMenu == 'sub3'} ? 'text-indigo-700' : 'text-gray-600 hover:bg-gray-50'">
        <div class="flex items-center gap-3">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"
                 stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                <!-- Icone SVG -->
            </svg>
            <span>Mon groupe</span>
        </div>
        <svg :class="menuOpen && 'rotate-90'" class="w-4 h-4 transition-transform duration-200"
             fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7"/>
        </svg>
    </button>
    <div x-show="menuOpen" x-collapse class="ml-6 space-y-0.5 mt-1">
        <a th:href="@{/hub/mon-groupe/sub1}"
           th:classappend="${activeMenu == 'sub1'} ? 'bg-indigo-50 text-indigo-700' : 'text-gray-500 hover:bg-gray-50'"
           class="block px-3 py-1.5 rounded-md text-xs font-medium">
            Sous-lien 1
        </a>
        <a th:href="@{/hub/mon-groupe/sub2}"
           th:classappend="${activeMenu == 'sub2'} ? 'bg-indigo-50 text-indigo-700' : 'text-gray-500 hover:bg-gray-50'"
           class="block px-3 py-1.5 rounded-md text-xs font-medium">
            Sous-lien 2
        </a>
    </div>
</div>
```

---

## 6. Pages types -- Templates complets

### 6.1 Page Liste (pattern CRUD)

**Template : `mon-module/list.html`**

```html
<!DOCTYPE html>
<html xmlns:th="http://www.thymeleaf.org"
      xmlns:layout="http://www.ultraq.net.nz/thymeleaf/layout"
      layout:decorate="~{layout/base}"
      lang="fr">
<head>
    <title>Mon module</title>
</head>
<body>

<div layout:fragment="content"
     x-data="{ showCreate: false }">

    <!-- Header -->
    <div class="flex justify-between items-center mb-6">
        <h1 class="text-2xl font-bold text-gray-900">Mon module</h1>
        <button @click="showCreate = !showCreate"
                class="px-4 py-2 bg-indigo-600 text-white rounded-lg text-sm font-medium
                       hover:bg-indigo-700 transition-colors flex items-center gap-2">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4"/>
            </svg>
            <span x-text="showCreate ? 'Annuler' : 'Creer'"></span>
        </button>
    </div>

    <!-- Formulaire collapsible -->
    <div x-show="showCreate"
         x-transition:enter="transition ease-out duration-200"
         x-transition:enter-start="opacity-0 -translate-y-2"
         x-transition:enter-end="opacity-100 translate-y-0"
         x-transition:leave="transition ease-in duration-150"
         x-transition:leave-start="opacity-100 translate-y-0"
         x-transition:leave-end="opacity-0 -translate-y-2"
         class="bg-white rounded-xl border border-gray-200 shadow-sm p-6 mb-6">
        <div class="flex items-center justify-between mb-4">
            <h2 class="text-base font-semibold text-gray-900">Nouvel element</h2>
            <button @click="showCreate = false" class="text-gray-400 hover:text-gray-600">
                <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"/>
                </svg>
            </button>
        </div>
        <form th:action="@{/hub/mon-module}" method="post">
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
                <div>
                    <label class="block text-sm font-medium text-gray-700 mb-1">Nom *</label>
                    <input type="text" name="name" required
                           class="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm
                                  focus:outline-none focus:border-indigo-500"/>
                </div>
                <div>
                    <label class="block text-sm font-medium text-gray-700 mb-1">Description</label>
                    <input type="text" name="description"
                           class="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm
                                  focus:outline-none focus:border-indigo-500"/>
                </div>
            </div>
            <div class="flex justify-end mt-4">
                <button type="submit"
                        class="px-6 py-2 bg-indigo-600 text-white rounded-lg text-sm font-medium
                               hover:bg-indigo-700 transition-colors">
                    Creer
                </button>
            </div>
        </form>
    </div>

    <!-- Table de donnees -->
    <div class="bg-white rounded-xl border border-gray-200 shadow-sm overflow-hidden">
        <table class="w-full text-sm">
            <thead class="bg-gray-50 border-b border-gray-200">
                <tr>
                    <th class="px-4 py-3 text-left font-medium text-gray-500">Nom</th>
                    <th class="px-4 py-3 text-left font-medium text-gray-500">Description</th>
                    <th class="px-4 py-3 text-left font-medium text-gray-500">Statut</th>
                    <th class="px-4 py-3 text-right font-medium text-gray-500">Actions</th>
                </tr>
            </thead>
            <tbody class="divide-y divide-gray-100">
                <tr th:each="item : ${items}" class="hover:bg-gray-50">
                    <td class="px-4 py-3">
                        <a th:href="@{/hub/mon-module/{id}(id=${item['id']})}"
                           class="font-medium text-indigo-600 hover:text-indigo-800 hover:underline"
                           th:text="${item['name']}"></a>
                    </td>
                    <td class="px-4 py-3 text-gray-600"
                        th:text="${item['description'] ?: '-'}"></td>
                    <td class="px-4 py-3">
                        <span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-green-100 text-green-700">
                            <span class="w-1.5 h-1.5 rounded-full bg-green-500"></span>Actif
                        </span>
                    </td>
                    <td class="px-4 py-3 text-right">
                        <form th:action="@{/hub/mon-module/{id}/delete(id=${item['id']})}"
                              method="post" class="inline"
                              x-data
                              @submit.prevent="if(confirm('Supprimer ?')) $el.submit()">
                            <button type="submit"
                                    class="px-2.5 py-1 text-xs text-red-600 border border-red-200 rounded
                                           hover:bg-red-50 transition-colors">
                                Supprimer
                            </button>
                        </form>
                    </td>
                </tr>
            </tbody>
        </table>

        <!-- Etat vide -->
        <div th:if="${#lists.isEmpty(items)}"
             class="text-center py-12 text-gray-500">
            <p class="text-sm">Aucun element.</p>
            <button @click="showCreate = true"
                    class="mt-2 text-sm text-indigo-600 hover:underline">
                Creer le premier element
            </button>
        </div>
    </div>

</div>

</body>
</html>
```

**Controller correspondant :**

```java
@Controller
@RequestMapping("/hub/mon-module")
public class MonModuleController {

    private final MonWorker monWorker;

    public MonModuleController(MonWorker monWorker) {
        this.monWorker = monWorker;
    }

    @GetMapping
    public String list(Model model) {
        ActionResult result = callAction(monWorker, "list_items");
        List<Map<String, Object>> items = getListFromResult(result, "items");

        model.addAttribute("items", items);
        model.addAttribute("pageTitle", "Mon module");
        model.addAttribute("activeMenu", "mon-module");

        return "mon-module/list";
    }

    @PostMapping
    public String create(@RequestParam("name") String name,
                         @RequestParam("description") String description,
                         RedirectAttributes redirectAttributes) {
        ActionResult result = callAction(monWorker, "create_item",
                ActionParameters.fromMap(Map.of("name", name, "description", description)));

        if (result.success()) {
            redirectAttributes.addFlashAttribute("success", "Element cree.");
        } else {
            redirectAttributes.addFlashAttribute("error", "Erreur : " + result.error());
        }
        return "redirect:/hub/mon-module";
    }

    @PostMapping("/{id}/delete")
    public String delete(@PathVariable String id, RedirectAttributes redirectAttributes) {
        ActionResult result = callAction(monWorker, "delete_item",
                ActionParameters.of("id", id));

        if (result.success()) {
            redirectAttributes.addFlashAttribute("success", "Element supprime.");
        } else {
            redirectAttributes.addFlashAttribute("error", "Erreur : " + result.error());
        }
        return "redirect:/hub/mon-module";
    }

    // --- Helpers ---

    private ActionResult callAction(ActionProvider worker, String actionName) {
        return callAction(worker, actionName, ActionParameters.empty());
    }

    private ActionResult callAction(ActionProvider worker, String actionName, ActionParameters params) {
        ActionContext ctx = ActionContext.internal("portal");
        try {
            ActionResult result = worker.executeAction(actionName, params, ctx);
            if (!result.success()) {
                log.warn("Action {} failed: {}", actionName, result.error());
            }
            return result;
        } catch (Exception e) {
            log.warn("Action {} threw: {}", actionName, e.getMessage());
            return ActionResult.ofFailure("Exception: " + e.getMessage());
        }
    }

    @SuppressWarnings("unchecked")
    private <T> List<T> getListFromResult(ActionResult result, String key) {
        if (result == null || !result.success() || result.data() == null) {
            return Collections.emptyList();
        }
        Object value = result.data().get(key);
        return value instanceof List<?> ? (List<T>) value : Collections.emptyList();
    }
}
```

### 6.2 Page Detail (pattern single entity)

**Template : `mon-module/detail.html`**

```html
<!DOCTYPE html>
<html xmlns:th="http://www.thymeleaf.org"
      xmlns:layout="http://www.ultraq.net.nz/thymeleaf/layout"
      layout:decorate="~{layout/base}"
      lang="fr">
<head>
    <title th:text="${item['name']}">Detail</title>
</head>
<body>

<div layout:fragment="content"
     x-data="{ confirmDelete: false }">

    <!-- Breadcrumb -->
    <div class="mb-6">
        <div class="flex items-center gap-2 text-sm text-gray-500 mb-2">
            <a th:href="@{/hub/mon-module}" class="hover:text-indigo-600">Mon module</a>
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7"/>
            </svg>
            <span th:text="${item['name']}" class="text-gray-900"></span>
        </div>
        <div class="flex items-center justify-between">
            <div class="flex items-center gap-3">
                <h1 class="text-2xl font-bold text-gray-900" th:text="${item['name']}"></h1>
                <!-- Badge statut -->
                <span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-green-100 text-green-700">
                    <span class="w-1.5 h-1.5 rounded-full bg-green-500"></span>Actif
                </span>
            </div>
            <div class="flex items-center gap-2">
                <button @click="confirmDelete = true"
                        class="px-3 py-1.5 text-sm bg-red-600 text-white rounded-lg hover:bg-red-700">
                    Supprimer
                </button>
            </div>
        </div>
    </div>

    <!-- Grille 2 colonnes (large) + 1 colonne (droite) -->
    <div class="grid grid-cols-1 lg:grid-cols-3 gap-6">

        <!-- Colonne gauche (2/3) -->
        <div class="lg:col-span-2 space-y-6">

            <!-- Carte informations -->
            <div class="bg-white rounded-xl border border-gray-200 shadow-sm p-6">
                <h2 class="text-base font-semibold text-gray-900 mb-4">Informations</h2>
                <dl class="grid grid-cols-2 gap-4 text-sm">
                    <div>
                        <dt class="text-gray-500">Nom</dt>
                        <dd class="text-gray-900 font-medium mt-0.5" th:text="${item['name']}"></dd>
                    </div>
                    <div>
                        <dt class="text-gray-500">Description</dt>
                        <dd class="text-gray-900 mt-0.5" th:text="${item['description'] ?: 'N/A'}"></dd>
                    </div>
                    <div>
                        <dt class="text-gray-500">Date de creation</dt>
                        <dd class="text-gray-900 mt-0.5" th:text="${item['created_at'] ?: 'N/A'}"></dd>
                    </div>
                </dl>
            </div>

            <!-- Section avec sous-elements et formulaire inline -->
            <div class="bg-white rounded-xl border border-gray-200 shadow-sm p-6">
                <h2 class="text-base font-semibold text-gray-900 mb-4">Elements associes</h2>
                <!-- Liste des elements avec bouton retirer -->
                <!-- Formulaire d'ajout en bas -->
            </div>

        </div>

        <!-- Colonne droite (1/3) -->
        <div class="space-y-6">

            <!-- Carte resume -->
            <div class="bg-white rounded-xl border border-gray-200 shadow-sm p-6">
                <h2 class="text-base font-semibold text-gray-900 mb-4">Resume</h2>
                <dl class="space-y-3 text-sm">
                    <div class="flex justify-between">
                        <dt class="text-gray-500">Propriete 1</dt>
                        <dd class="text-gray-900 font-medium" th:text="${item['prop1'] ?: '0'}"></dd>
                    </div>
                    <div class="flex justify-between">
                        <dt class="text-gray-500">Propriete 2</dt>
                        <dd class="text-gray-900 font-medium" th:text="${item['prop2'] ?: '0'}"></dd>
                    </div>
                </dl>
            </div>

            <!-- Zone de danger -->
            <div class="bg-white rounded-xl border border-red-200 shadow-sm p-6">
                <h2 class="text-base font-semibold text-red-700 mb-4">Zone de danger</h2>
                <p class="text-sm text-gray-600 mb-4">
                    La suppression est irreversible.
                </p>
                <button @click="confirmDelete = true"
                        class="block w-full text-center px-4 py-2.5 text-sm text-red-700 border border-red-300
                               rounded-lg hover:bg-red-50 transition-colors font-medium">
                    Supprimer definitivement
                </button>
            </div>

        </div>

    </div>

    <!-- Modale de confirmation -->
    <div x-show="confirmDelete" x-transition class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
         @keydown.escape.window="confirmDelete = false">
        <div class="bg-white rounded-xl shadow-xl p-6 max-w-md w-full mx-4" @click.outside="confirmDelete = false">
            <h3 class="text-lg font-semibold text-gray-900 mb-2">Confirmer la suppression</h3>
            <p class="text-sm text-gray-600 mb-6">
                Supprimer <strong th:text="${item['name']}"></strong> ? Cette action est irreversible.
            </p>
            <div class="flex justify-end gap-3">
                <button @click="confirmDelete = false"
                        class="px-4 py-2 text-sm text-gray-700 border border-gray-300 rounded-lg hover:bg-gray-50">
                    Annuler
                </button>
                <form th:action="@{/hub/mon-module/{id}/delete(id=${item['id']})}" method="post" class="inline">
                    <button type="submit"
                            class="px-4 py-2 text-sm bg-red-600 text-white rounded-lg hover:bg-red-700 font-medium">
                        Supprimer definitivement
                    </button>
                </form>
            </div>
        </div>
    </div>

</div>

</body>
</html>
```

**Controller correspondant :**

```java
@GetMapping("/{id}")
public String detail(@PathVariable String id, Model model) {
    ActionResult result = callAction(monWorker, "get_item",
            ActionParameters.of("id", id));
    Map<String, Object> item = result.success() ? result.data() : Map.of();

    model.addAttribute("item", item);
    model.addAttribute("pageTitle", (String) item.getOrDefault("name", "Detail"));
    model.addAttribute("activeMenu", "mon-module");

    return "mon-module/detail";
}
```

### 6.3 Page Dashboard

Voir section 5.2 pour les stat cards. Le pattern complet est :

```java
@GetMapping({"", "/"})
public String dashboard(Model model) {
    // Collecter les stats depuis les Workers
    ActionResult appsResult = callAction(monWorker, "list_items");
    List<Map<String, Object>> items = getListFromResult(appsResult, "items");

    // Construire les stats
    model.addAttribute("stats", buildStats(items));
    model.addAttribute("items", items);
    model.addAttribute("alerts", buildAlerts(items));
    model.addAttribute("now", LocalDateTime.now());
    model.addAttribute("pageTitle", "Dashboard");
    model.addAttribute("activeMenu", "dashboard");

    return "dashboard";
}
```

Template : voir le code complet de `dashboard.html` dans les sources SocleHub (section `grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4` pour les stats, puis les alertes, puis la grille d'applications).

### 6.4 Page Login (standalone, pas de layout)

La page login n'herite PAS de `base.html` car elle est affichee hors connexion.

```html
<!DOCTYPE html>
<html xmlns:th="http://www.thymeleaf.org" lang="fr">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Connexion &mdash; SocleHub</title>

    <script src="https://cdn.tailwindcss.com"></script>
    <script defer src="https://cdn.jsdelivr.net/npm/alpinejs@3.x.x/dist/cdn.min.js"></script>
</head>
<body class="bg-gray-50 flex items-center justify-center min-h-screen">

    <div class="bg-white rounded-2xl border border-gray-200 shadow-lg p-10 w-96">

        <!-- Branding -->
        <div class="text-center mb-8">
            <div class="mb-3">
                <svg class="w-12 h-12 mx-auto text-indigo-600" viewBox="0 0 24 24" fill="none"
                     stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <path d="M12 2L2 7l10 5 10-5-10-5z"/>
                    <path d="M2 17l10 5 10-5"/>
                    <path d="M2 12l10 5 10-5"/>
                </svg>
            </div>
            <h1 class="text-2xl font-bold text-gray-900">SocleHub</h1>
            <p class="text-sm text-gray-500 mt-1" th:text="${hubDomain}">hub.example.com</p>
        </div>

        <!-- Message d'erreur -->
        <div th:if="${param.error}"
             class="mb-4 p-3 bg-red-50 border border-red-200 rounded-lg
                    text-red-700 text-sm text-center">
            Email ou mot de passe incorrect.
        </div>

        <!-- Message de deconnexion -->
        <div th:if="${param.logout}"
             class="mb-4 p-3 bg-green-50 border border-green-200 rounded-lg
                    text-green-700 text-sm text-center">
            Vous avez ete deconnecte.
        </div>

        <!-- Formulaire -->
        <form th:action="@{/hub/login}" method="post">
            <input type="hidden" th:name="${_csrf?.parameterName}" th:value="${_csrf?.token}"/>

            <div class="mb-4">
                <label for="email" class="block text-sm font-medium text-gray-700 mb-1">
                    Email
                </label>
                <input type="email" id="email" name="email" autocomplete="email"
                       required autofocus
                       class="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm
                              focus:outline-none focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500"
                       placeholder="admin@example.com">
            </div>

            <div class="mb-6">
                <label for="password" class="block text-sm font-medium text-gray-700 mb-1">
                    Mot de passe
                </label>
                <input type="password" id="password" name="password" autocomplete="current-password"
                       required
                       class="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm
                              focus:outline-none focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500"
                       placeholder="********">
            </div>

            <button type="submit"
                    class="w-full bg-indigo-600 text-white py-2.5 rounded-lg text-sm
                           font-medium hover:bg-indigo-700 transition-colors
                           focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:ring-offset-2">
                Se connecter
            </button>
        </form>

    </div>

</body>
</html>
```

**Controller login (dans SecurityConfig, pas de controller explicite) :**

Le login est gere automatiquement par Spring Security via `.loginPage("/hub/login")`. Cependant, il faut un GET handler pour servir la page :

```java
@Controller
public class LoginController {

    @GetMapping("/hub/login")
    public String loginPage(Model model) {
        model.addAttribute("hubDomain", "admin.mondomaine.com");
        return "auth/login";
    }
}
```

---

## 7. Architecture Controller -- Pattern V005

### 7.1 Le pattern PortalController

Dans SocleHub, les controleurs Thymeleaf n'appellent pas d'API REST. Ils injectent directement les Workers V005 (car tout est dans le meme JAR) et appellent `executeAction()`.

```java
@Controller
@RequestMapping("/hub")
public class PortalDashboardController {

    // Injection directe des Workers par constructeur
    private final DockerWorker dockerWorker;
    private final IamWorker iamWorker;

    public PortalDashboardController(DockerWorker dockerWorker,
                                     IamWorker iamWorker) {
        this.dockerWorker = dockerWorker;
        this.iamWorker = iamWorker;
    }

    @GetMapping({"", "/"})
    public String dashboard(Model model) {
        // Appeler une action sur un Worker
        ActionResult result = callAction("docker", dockerWorker, "list_apps");

        // Extraire les donnees du resultat
        List<Map<String, Object>> apps = getListFromResult(result, "apps");

        // Passer au modele Thymeleaf
        model.addAttribute("apps", apps);
        model.addAttribute("pageTitle", "Dashboard");
        model.addAttribute("activeMenu", "dashboard");

        return "dashboard";
    }
}
```

### 7.2 Helper callAction

Ce helper centralise l'appel aux Workers avec gestion d'erreur.

```java
private ActionResult callAction(String workerLabel,
                                ActionProvider worker,
                                String actionName,
                                ActionParameters params) {
    ActionContext ctx = ActionContext.internal("portal");
    try {
        ActionResult result = worker.executeAction(actionName, params, ctx);
        if (!result.success()) {
            log.warn("Action {}/{} failed: {}", workerLabel, actionName, result.error());
        }
        return result;
    } catch (Exception e) {
        log.warn("Action {}/{} threw exception: {}", workerLabel, actionName, e.getMessage(), e);
        return ActionResult.ofFailure("Exception: " + e.getMessage());
    }
}

private ActionResult callAction(String workerLabel,
                                ActionProvider worker,
                                String actionName) {
    return callAction(workerLabel, worker, actionName, ActionParameters.empty());
}
```

**Helpers d'extraction de donnees :**

```java
@SuppressWarnings("unchecked")
private <T> List<T> getListFromResult(ActionResult result, String key) {
    if (result == null || !result.success() || result.data() == null) {
        return Collections.emptyList();
    }
    Object value = result.data().get(key);
    if (value instanceof List<?>) {
        return (List<T>) value;
    }
    return Collections.emptyList();
}

private int getIntFromResult(ActionResult result, String key, int defaultValue) {
    if (result == null || !result.success() || result.data() == null) {
        return defaultValue;
    }
    Object value = result.data().get(key);
    if (value instanceof Number num) {
        return num.intValue();
    }
    return defaultValue;
}

private String getStringFromResult(ActionResult result, String key, String defaultValue) {
    if (result == null || !result.success() || result.data() == null) {
        return defaultValue;
    }
    Object value = result.data().get(key);
    if (value instanceof String str) {
        return str;
    }
    return defaultValue;
}
```

### 7.3 Pattern POST + Flash + Redirect

Toutes les actions destructives suivent le pattern PRG (Post-Redirect-Get) :

```java
@PostMapping("/mon-module")
public String create(@RequestParam("name") String name,
                     @RequestParam("description") String description,
                     RedirectAttributes redirectAttributes) {

    // 1. Construire les parametres
    ActionParameters params = ActionParameters.fromMap(Map.of(
            "name", name,
            "description", description
    ));

    // 2. Appeler l'action du Worker
    ActionResult result = callAction("mon-worker", monWorker, "create_item", params);

    // 3. Message flash selon le resultat
    if (result.success()) {
        redirectAttributes.addFlashAttribute("success",
                "Element " + name + " cree avec succes.");
    } else {
        redirectAttributes.addFlashAttribute("error",
                "Erreur creation : " + result.error());
    }

    // 4. Redirect (PRG pattern)
    return "redirect:/hub/mon-module";
}
```

**Pour un POST qui renvoie vers la page detail :**

```java
@PostMapping("/mon-module/{id}/action")
public String doAction(@PathVariable String id,
                       RedirectAttributes redirectAttributes) {

    ActionResult result = callAction("worker", monWorker, "do_action",
            ActionParameters.of("id", id));

    if (result.success()) {
        redirectAttributes.addFlashAttribute("success", "Action reussie.");
    } else {
        redirectAttributes.addFlashAttribute("error", "Erreur : " + result.error());
    }

    return "redirect:/hub/mon-module/" + id;
}
```

### 7.4 Le WorkerActionController REST

Ce controller expose **automatiquement** toutes les actions de tous les Workers via une API REST.
Il est utilise pour les appels AJAX ou les integrations externes.

```java
package eu.lmvi.monprojet.controller;

import eu.lmvi.socle.action.ActionExecutor;
import eu.lmvi.socle.action.WorkerActionRegistry;
import eu.lmvi.socle.action.model.ActionContext;
import eu.lmvi.socle.action.model.ActionMetadata;
import eu.lmvi.socle.action.model.ActionParameters;
import eu.lmvi.socle.action.model.ActionResult;
import eu.lmvi.socle.action.model.ExecutionMode;
import eu.lmvi.socle.action.model.JobInfo;
import eu.lmvi.socle.action.model.WorkerActionsInfo;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.*;

import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;

/**
 * API REST pour les actions Worker.
 *
 *   GET  /api/actions                    -- lister tous les workers et actions
 *   GET  /api/actions/{worker}           -- lister les actions d'un worker
 *   POST /api/actions/{worker}/{action}  -- executer une action
 *   GET  /api/actions/jobs/{jobId}       -- statut d'un job async
 */
@RestController
@RequestMapping("/api/actions")
public class WorkerActionController {

    private static final Logger log = LoggerFactory.getLogger(WorkerActionController.class);

    private final ActionExecutor actionExecutor;
    private final WorkerActionRegistry registry;

    public WorkerActionController(@Autowired(required = false) ActionExecutor actionExecutor,
                                  @Autowired(required = false) WorkerActionRegistry registry) {
        this.actionExecutor = actionExecutor;
        this.registry = registry;
    }

    @GetMapping
    public ResponseEntity<?> listAll() {
        if (registry == null) {
            return ResponseEntity.status(503).body(Map.of("error", "WorkerActionRegistry not available"));
        }
        List<WorkerActionsInfo> all = registry.listAllActions();
        return ResponseEntity.ok(Map.of(
                "workers", all,
                "totalActions", registry.getTotalActionCount()
        ));
    }

    @GetMapping("/{worker}")
    public ResponseEntity<?> listWorkerActions(@PathVariable String worker) {
        if (registry == null) {
            return ResponseEntity.status(503).body(Map.of("error", "WorkerActionRegistry not available"));
        }
        List<ActionMetadata> actions = registry.getWorkerActions(worker);
        if (actions.isEmpty()) {
            return ResponseEntity.notFound().build();
        }
        return ResponseEntity.ok(Map.of("worker", worker, "actions", actions));
    }

    @PostMapping("/{worker}/{action}")
    public ResponseEntity<?> executeAction(
            @PathVariable String worker,
            @PathVariable String action,
            @RequestBody(required = false) Map<String, Object> body) {

        if (actionExecutor == null) {
            return ResponseEntity.status(503).body(Map.of("error", "ActionExecutor not available"));
        }

        Optional<ActionMetadata> metaOpt = registry.getActionMetadata(worker, action);
        if (metaOpt.isEmpty()) {
            return ResponseEntity.notFound().build();
        }

        ActionMetadata meta = metaOpt.get();
        ActionParameters params = (body != null && !body.isEmpty())
                ? ActionParameters.fromMap(body)
                : ActionParameters.empty();
        ActionContext ctx = ActionContext.internal("rest-api");

        log.info("REST action: {}/{} params={}", worker, action, body);

        try {
            if (meta.mode() == ExecutionMode.ASYNC) {
                JobInfo job = actionExecutor.executeAsync(worker, action, params, ctx);
                Map<String, Object> result = new HashMap<>();
                result.put("status", "accepted");
                result.put("jobId", job.jobId());
                result.put("worker", worker);
                result.put("action", action);
                return ResponseEntity.accepted().body(result);
            } else {
                ActionResult result = actionExecutor.executeSync(worker, action, params, ctx);
                Map<String, Object> response = new HashMap<>();
                response.put("success", result.success());
                response.put("worker", worker);
                response.put("action", action);
                if (result.success()) {
                    response.put("data", result.data());
                } else {
                    response.put("error", result.error());
                }
                return ResponseEntity.ok(response);
            }
        } catch (Exception e) {
            log.error("Action execution failed: {}/{}: {}", worker, action, e.getMessage(), e);
            return ResponseEntity.internalServerError().body(Map.of(
                    "success", false,
                    "error", e.getMessage() != null ? e.getMessage() : "Unknown error"
            ));
        }
    }

    @GetMapping("/jobs/{jobId}")
    public ResponseEntity<?> getJobStatus(@PathVariable String jobId) {
        if (actionExecutor == null) {
            return ResponseEntity.status(503).body(Map.of("error", "ActionExecutor not available"));
        }
        return actionExecutor.getJobStatus(jobId)
                .map(status -> ResponseEntity.ok((Object) status))
                .orElse(ResponseEntity.notFound().build());
    }
}
```

---

## 8. Integration Worker V005

### 8.1 Comment un Worker alimente l'IHM

Le flux de donnees est le suivant :

```
Worker (implements ActionProvider)
    |
    |-- executeAction("list_items", params, ctx)
    |       |
    |       v
    |   ActionResult.ofSuccess(Map.of("items", List<Map<String, Object>>))
    |
Controller (@Controller)
    |
    |-- ActionResult result = worker.executeAction("list_items", params, ctx);
    |-- List<Map<String, Object>> items = (List) result.data().get("items");
    |-- model.addAttribute("items", items);
    |
Template (Thymeleaf)
    |
    |-- th:each="item : ${items}"
    |-- th:text="${item['name']}"       <-- notation bracket pour Map
    |-- th:text="${item['created_at']}"
```

**Important** : les Workers retournent des `Map<String, Object>`. Dans Thymeleaf, on accede aux cles avec la **notation bracket** : `${item['key']}`, pas `${item.key}` (qui appelle un getter Java).

### 8.2 Exemple complet : nouvelle entite

Voici les etapes pour creer une fonctionnalite CRUD complete "Projets".

**Etape 1 : Le Worker**

```java
@Component
public class ProjectWorker extends AbstractPassiveActionWorker {

    @Override
    public String getName() { return "ProjectWorker"; }

    @Override
    public List<ActionMetadata> getActions() {
        return List.of(
            ActionMetadata.sync("list_projects", "List all projects"),
            ActionMetadata.sync("get_project", "Get a single project"),
            ActionMetadata.sync("create_project", "Create a project"),
            ActionMetadata.sync("delete_project", "Delete a project")
        );
    }

    @Override
    public ActionResult executeAction(String actionName, ActionParameters params, ActionContext ctx) {
        return switch (actionName) {
            case "list_projects" -> listProjects();
            case "get_project" -> getProject(params.getString("id"));
            case "create_project" -> createProject(params);
            case "delete_project" -> deleteProject(params.getString("id"));
            default -> ActionResult.ofFailure("Unknown action: " + actionName);
        };
    }

    private ActionResult listProjects() {
        // Retourner les donnees sous forme de Map
        List<Map<String, Object>> projects = // ... charger depuis DB
        return ActionResult.ofSuccess(Map.of("projects", projects));
    }

    // ... autres methodes
}
```

**Etape 2 : Le Controller**

```java
@Controller
@RequestMapping("/hub/projects")
public class PortalProjectController {

    private final ProjectWorker projectWorker;

    public PortalProjectController(ProjectWorker projectWorker) {
        this.projectWorker = projectWorker;
    }

    @GetMapping
    public String list(Model model) {
        ActionResult result = callAction(projectWorker, "list_projects");
        model.addAttribute("projects", getListFromResult(result, "projects"));
        model.addAttribute("pageTitle", "Projets");
        model.addAttribute("activeMenu", "projects");
        return "projects/list";
    }

    @GetMapping("/{id}")
    public String detail(@PathVariable String id, Model model) {
        ActionResult result = callAction(projectWorker, "get_project",
                ActionParameters.of("id", id));
        model.addAttribute("project", result.success() ? result.data() : Map.of());
        model.addAttribute("pageTitle", "Projet");
        model.addAttribute("activeMenu", "projects");
        return "projects/detail";
    }

    @PostMapping
    public String create(@RequestParam("name") String name,
                         @RequestParam("description") String description,
                         RedirectAttributes redirectAttributes) {
        ActionResult result = callAction(projectWorker, "create_project",
                ActionParameters.fromMap(Map.of("name", name, "description", description)));
        if (result.success()) {
            redirectAttributes.addFlashAttribute("success", "Projet cree.");
        } else {
            redirectAttributes.addFlashAttribute("error", "Erreur : " + result.error());
        }
        return "redirect:/hub/projects";
    }

    @PostMapping("/{id}/delete")
    public String delete(@PathVariable String id, RedirectAttributes redirectAttributes) {
        ActionResult result = callAction(projectWorker, "delete_project",
                ActionParameters.of("id", id));
        if (result.success()) {
            redirectAttributes.addFlashAttribute("success", "Projet supprime.");
        } else {
            redirectAttributes.addFlashAttribute("error", "Erreur : " + result.error());
        }
        return "redirect:/hub/projects";
    }

    // ... helpers callAction, getListFromResult (meme pattern que section 7.2)
}
```

**Etape 3 : Les Templates**

Utiliser les patterns de la section 6.1 (liste) et 6.2 (detail).

**Etape 4 : Entree sidebar**

Ajouter dans `sidebar.html` :

```html
<a th:href="@{/hub/projects}"
   th:classappend="${activeMenu == 'projects'} ? 'bg-indigo-50 text-indigo-700' : 'text-gray-600 hover:bg-gray-50'"
   class="flex items-center gap-3 px-3 py-2 rounded-md text-sm font-medium">
    <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"
         stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
        <path d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z"/>
    </svg>
    <span>Projets</span>
</a>
```

**Etape 5 : Securite (si necessaire)**

La regle `.requestMatchers("/hub/**").authenticated()` protege deja toutes les pages `/hub/**`. Si vous avez besoin de restrictions par role :

```java
.requestMatchers("/hub/admin/**").hasRole("ADMIN")
.requestMatchers("/hub/**").authenticated()
```

---

## 9. JavaScript helpers (portal.js)

Le fichier `portal.js` fournit 4 utilitaires. Le charger dans `base.html` :

```html
<script src="/js/portal.js"></script>
```

**Code complet :**

```javascript
/**
 * SocleHub Portal -- JavaScript Helpers
 *
 * Fournit :
 *  - Toast notifications
 *  - SSE (Server-Sent Events) connection helper
 *  - Fetch wrapper avec gestion d'erreur
 *  - Confirm dialog helper
 */

// --- Toast Notifications ---------------------------------------------------

const Toast = (() => {
    let container = null;

    function ensureContainer() {
        if (!container) {
            container = document.createElement('div');
            container.id = 'toast-container';
            container.className = 'fixed top-4 right-4 z-50 flex flex-col gap-2';
            document.body.appendChild(container);
        }
        return container;
    }

    function show(message, type = 'info', durationMs = 4000) {
        const wrapper = ensureContainer();
        const colors = {
            info:    'bg-blue-600 text-white',
            success: 'bg-green-600 text-white',
            warning: 'bg-yellow-500 text-gray-900',
            error:   'bg-red-600 text-white',
        };

        const toast = document.createElement('div');
        toast.className = `toast-enter flex items-center gap-2 px-4 py-3 rounded-lg shadow-lg text-sm font-medium ${colors[type] || colors.info}`;
        toast.innerHTML = `<span>${escapeHtml(message)}</span>`;
        wrapper.appendChild(toast);

        const timer = setTimeout(() => dismiss(toast), durationMs);
        toast.addEventListener('click', () => {
            clearTimeout(timer);
            dismiss(toast);
        });
        return toast;
    }

    function dismiss(toast) {
        toast.classList.remove('toast-enter');
        toast.classList.add('toast-exit');
        toast.addEventListener('animationend', () => toast.remove(), { once: true });
        setTimeout(() => { if (toast.parentNode) toast.remove(); }, 500);
    }

    function info(msg, ms)    { return show(msg, 'info', ms); }
    function success(msg, ms) { return show(msg, 'success', ms); }
    function warning(msg, ms) { return show(msg, 'warning', ms); }
    function error(msg, ms)   { return show(msg, 'error', ms); }

    return { show, info, success, warning, error };
})();


// --- SSE Connection Helper -------------------------------------------------

/**
 * Connecter a un endpoint SSE avec reconnexion automatique.
 *
 * @param {string}   url         URL de l'endpoint SSE
 * @param {Object}   handlers    Map event-type -> callback(data)
 * @param {Object}   [opts]      Options
 * @returns {{ close: Function }} Handle pour fermer la connexion
 */
function sseConnect(url, handlers = {}, opts = {}) {
    const reconnectMs = opts.reconnectMs ?? 3000;
    let source = null;
    let closed = false;

    function connect() {
        if (closed) return;
        source = new EventSource(url);

        source.onopen = () => { if (opts.onOpen) opts.onOpen(); };

        Object.keys(handlers).forEach((eventType) => {
            source.addEventListener(eventType, (event) => {
                try {
                    handlers[eventType](JSON.parse(event.data), event);
                } catch {
                    handlers[eventType](event.data, event);
                }
            });
        });

        if (handlers.message) {
            source.onmessage = (event) => {
                try { handlers.message(JSON.parse(event.data), event); }
                catch { handlers.message(event.data, event); }
            };
        }

        source.onerror = (err) => {
            if (opts.onError) opts.onError(err);
            source.close();
            if (!closed) setTimeout(connect, reconnectMs);
        };
    }

    connect();
    return { close() { closed = true; if (source) source.close(); } };
}


// --- Fetch Wrapper ---------------------------------------------------------

/**
 * Wrapper fetch() avec gestion JSON et toast d'erreur.
 */
async function api(url, opts = {}) {
    const headers = {
        'Content-Type': 'application/json',
        'Accept': 'application/json',
        ...(opts.headers || {})
    };

    let body = opts.body;
    if (body && typeof body === 'object' && !(body instanceof FormData)) {
        body = JSON.stringify(body);
    }

    try {
        const response = await fetch(url, { ...opts, headers, body });
        if (!response.ok) {
            let errorMsg = `HTTP ${response.status}`;
            try { const err = await response.json(); errorMsg = err.message || err.error || errorMsg; }
            catch { errorMsg += ` ${response.statusText}`; }
            Toast.error(errorMsg);
            return null;
        }
        if (response.status === 204) return null;
        const ct = response.headers.get('Content-Type') || '';
        return ct.includes('application/json') ? await response.json() : await response.text();
    } catch (err) {
        Toast.error(`Network error: ${err.message}`);
        return null;
    }
}


// --- Confirm Dialog --------------------------------------------------------

/**
 * Modale de confirmation programmatique.
 * Retourne une Promise<boolean>.
 */
function confirmDialog({ title, message, confirmText = 'Confirm', cancelText = 'Cancel',
                         confirmClass = 'bg-red-600 hover:bg-red-700 text-white' } = {}) {
    return new Promise((resolve) => {
        const backdrop = document.createElement('div');
        backdrop.className = 'fixed inset-0 z-50 flex items-center justify-center bg-black/50';

        const card = document.createElement('div');
        card.className = 'bg-white rounded-xl shadow-2xl p-6 max-w-md w-full mx-4 space-y-4';
        card.innerHTML = `
            <h3 class="text-lg font-semibold text-gray-900">${escapeHtml(title || 'Confirm')}</h3>
            <p class="text-sm text-gray-600">${escapeHtml(message || 'Are you sure?')}</p>
            <div class="flex justify-end gap-3 pt-2">
                <button id="confirm-cancel" class="px-4 py-2 text-sm rounded-lg border border-gray-300 text-gray-700 hover:bg-gray-100">
                    ${escapeHtml(cancelText)}
                </button>
                <button id="confirm-ok" class="px-4 py-2 text-sm rounded-lg font-medium ${confirmClass}">
                    ${escapeHtml(confirmText)}
                </button>
            </div>`;

        backdrop.appendChild(card);
        document.body.appendChild(backdrop);

        function cleanup(result) { backdrop.remove(); resolve(result); }

        card.querySelector('#confirm-ok').addEventListener('click', () => cleanup(true));
        card.querySelector('#confirm-cancel').addEventListener('click', () => cleanup(false));
        backdrop.addEventListener('click', (e) => { if (e.target === backdrop) cleanup(false); });

        function onKey(e) {
            if (e.key === 'Escape') { document.removeEventListener('keydown', onKey); cleanup(false); }
        }
        document.addEventListener('keydown', onKey);
    });
}


// --- Utilitaire ------------------------------------------------------------

function escapeHtml(str) {
    const div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
}
```

**Utilisation dans un template :**

```html
<!-- Toast depuis un bouton Alpine.js -->
<button @click="Toast.success('Operation reussie')">Test toast</button>

<!-- Confirm dialog puis POST -->
<button @click="if(await confirmDialog({title: 'Supprimer ?', message: 'Irreversible.', confirmText: 'Oui'})) $refs.deleteForm.submit()">
    Supprimer
</button>

<!-- Appel API REST -->
<button @click="const data = await api('/api/actions/docker/list_apps'); console.log(data)">
    Charger via API
</button>
```

---

## 10. CSS complementaire (portal.css)

Ce fichier ajoute des styles non disponibles via Tailwind. Le charger dans `base.html` :

```html
<link rel="stylesheet" href="/css/portal.css">
```

**Code complet :**

```css
/* ==========================================================================
   SocleHub Portal -- CSS complementaire (complete Tailwind)
   ========================================================================== */

/* --- Scrollbar personnalisee (fin et discrete) ----------------------------- */

::-webkit-scrollbar {
    width: 6px;
    height: 6px;
}
::-webkit-scrollbar-track {
    background: transparent;
}
::-webkit-scrollbar-thumb {
    background: rgba(148, 163, 184, 0.4);
    border-radius: 3px;
}
::-webkit-scrollbar-thumb:hover {
    background: rgba(148, 163, 184, 0.6);
}
/* Firefox */
* {
    scrollbar-width: thin;
    scrollbar-color: rgba(148, 163, 184, 0.4) transparent;
}


/* --- Toast Animations (entree par la droite, sortie vers la droite) -------- */

.toast-enter {
    animation: toast-slide-in 0.3s ease-out forwards;
}
.toast-exit {
    animation: toast-slide-out 0.25s ease-in forwards;
}

@keyframes toast-slide-in {
    from { opacity: 0; transform: translateX(100%); }
    to   { opacity: 1; transform: translateX(0); }
}
@keyframes toast-slide-out {
    from { opacity: 1; transform: translateX(0); }
    to   { opacity: 0; transform: translateX(100%); }
}


/* --- Police terminal (pour les zones de logs et code) ---------------------- */

.font-terminal {
    font-family: 'JetBrains Mono', 'Fira Code', 'Cascadia Code', 'Source Code Pro',
                 'Menlo', 'Consolas', monospace;
    font-variant-ligatures: common-ligatures;
}

.terminal-output {
    font-family: 'JetBrains Mono', 'Fira Code', 'Cascadia Code', 'Source Code Pro',
                 'Menlo', 'Consolas', monospace;
    font-size: 0.8125rem;
    line-height: 1.5;
    background: #0f172a;
    color: #e2e8f0;
    padding: 1rem;
    border-radius: 0.5rem;
    overflow-x: auto;
    white-space: pre-wrap;
    word-break: break-word;
}


/* --- Indicateur de statut (dot pulsant) ------------------------------------ */

.status-dot {
    width: 10px;
    height: 10px;
    border-radius: 50%;
    display: inline-block;
}
.status-dot.running {
    background: #22c55e;
    box-shadow: 0 0 0 0 rgba(34, 197, 94, 0.6);
    animation: status-pulse 2s infinite;
}
.status-dot.stopped {
    background: #ef4444;
}
.status-dot.starting {
    background: #eab308;
    animation: status-pulse 1s infinite;
}

@keyframes status-pulse {
    0%   { box-shadow: 0 0 0 0 rgba(34, 197, 94, 0.5); }
    70%  { box-shadow: 0 0 0 8px rgba(34, 197, 94, 0); }
    100% { box-shadow: 0 0 0 0 rgba(34, 197, 94, 0); }
}


/* --- Skeleton Loading (placeholder de chargement) -------------------------- */

.skeleton {
    background: linear-gradient(90deg, #e2e8f0 25%, #f1f5f9 50%, #e2e8f0 75%);
    background-size: 200% 100%;
    animation: skeleton-shimmer 1.5s ease-in-out infinite;
    border-radius: 0.375rem;
}

@keyframes skeleton-shimmer {
    0%   { background-position: 200% 0; }
    100% { background-position: -200% 0; }
}


/* --- Transition d'apparition de page --------------------------------------- */

.fade-in {
    animation: fade-in 0.2s ease-out forwards;
}

@keyframes fade-in {
    from { opacity: 0; transform: translateY(4px); }
    to   { opacity: 1; transform: translateY(0); }
}
```

---

## 11. Conventions et regles absolues

### Regles de style

1. **Jamais de CSS custom** pour ce que Tailwind peut faire. Utiliser `portal.css` uniquement pour les animations, scrollbars et fontes.
2. **Toujours Alpine.js** pour l'interactivite (toggles, dropdowns, modales, collapsibles). Pas de jQuery, pas de framework JS.
3. **Toujours `layout:decorate="~{layout/base}"`** sur chaque page (sauf login).
4. **Notation bracket** pour les Map : `${item['key']}` pas `${item.key}`.

### Regles de controller

5. **Toujours definir `pageTitle`** dans le modele : `model.addAttribute("pageTitle", "Mon titre")`.
6. **Toujours definir `activeMenu`** dans le modele : `model.addAttribute("activeMenu", "mon-module")`.
7. **Flash messages pour le feedback** : jamais d'affichage inline d'erreur dans la page. Utiliser `redirectAttributes.addFlashAttribute("success", ...)` ou `"error"`.
8. **Pattern PRG** (Post-Redirect-Get) pour tous les POST : toujours `return "redirect:/hub/..."`.

### Regles d'architecture

9. **Worker -> Controller -> Template** : les Workers produisent les donnees, les Controllers les injectent dans le modele, les Templates les affichent.
10. **Pas d'appels REST internes** : les Controllers injectent les Workers directement (meme JAR).
11. **callAction helper** dans chaque Controller pour encapsuler la gestion d'erreur.
12. **1 Controller par module** : `PortalProjectController`, `PortalIamController`, etc.

### Regles de template

13. **Empty state** obligatoire dans chaque tableau : `th:if="${#lists.isEmpty(items)}"`.
14. **CSRF token** obligatoire dans chaque `<form method="post">` qui n'est pas dans une zone `csrf.ignoringRequestMatchers`.
15. **Bouton de suppression** toujours avec `confirm()` ou modale Alpine.js.
16. **SVG inline** pour les icones (pas de font icons). Taille standard : `w-5 h-5` sidebar, `w-4 h-4` actions.

### Regles de nommage

17. **Routes** : `/hub/module` (liste), `/hub/module/{id}` (detail), `/hub/module/{id}/action` (POST).
18. **Templates** : `module/list.html`, `module/detail.html`.
19. **activeMenu** : correspond au nom du lien sidebar (`'dashboard'`, `'apps'`, `'users'`, etc.).
20. **Package Java** : toujours `eu.lmvi`, jamais `com.example`.

---

## 12. Checklist nouvelle page

Pour ajouter une nouvelle page a votre application SocleHub :

- [ ] **Template** : Creer `templates/mon-module/list.html`
- [ ] **Layout** : Ajouter `layout:decorate="~{layout/base}"` et `layout:fragment="content"`
- [ ] **Controller** : Creer `PortalMonModuleController` avec `@Controller` et `@RequestMapping("/hub/mon-module")`
- [ ] **GET handler** : Ajouter `@GetMapping` qui retourne le nom du template
- [ ] **pageTitle** : `model.addAttribute("pageTitle", "Mon module")`
- [ ] **activeMenu** : `model.addAttribute("activeMenu", "mon-module")`
- [ ] **Sidebar** : Ajouter le lien dans `sidebar.html` avec le bon `activeMenu`
- [ ] **Donnees** : Injecter le Worker et appeler `callAction()` pour les donnees
- [ ] **Empty state** : Ajouter un bloc quand la liste est vide
- [ ] **Formulaire creation** : Pattern collapsible si necessaire
- [ ] **Actions POST** : Pattern PRG avec `RedirectAttributes` flash
- [ ] **CSRF** : Verifier que les forms POST ont le token CSRF (ou que la route est dans `ignoringRequestMatchers`)
- [ ] **Securite** : Verifier que `/hub/mon-module/**` est bien protege (il l'est par defaut avec `.requestMatchers("/hub/**").authenticated()`)
- [ ] **Tests** : Verifier les flash messages (succes et erreur)
- [ ] **Tests** : Verifier l'etat vide
- [ ] **Tests** : Verifier la page de detail si applicable

---

## Annexe A : Icones SVG courantes

Les icones utilisees dans SocleHub sont des SVG inline (pas de font icons). Voici les plus courantes :

```html
<!-- Home / Dashboard -->
<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"
     stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
    <path d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-4 0a1 1 0 01-1-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 01-1 1h-2z"/>
</svg>

<!-- Cube / Applications -->
<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"
     stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
    <path d="M20 7l-8-4-8 4m16 0l-8 4m8-4v10l-8 4m0-10L4 7m8 4v10M4 7v10l8 4"/>
</svg>

<!-- Users / IAM -->
<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"
     stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
    <path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2"/>
    <circle cx="9" cy="7" r="4"/>
    <path d="M23 21v-2a4 4 0 00-3-3.87"/>
    <path d="M16 3.13a4 4 0 010 7.75"/>
</svg>

<!-- Globe / DNS -->
<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"
     stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
    <circle cx="12" cy="12" r="10"/>
    <path d="M2 12h20"/>
    <path d="M12 2a15.3 15.3 0 014 10 15.3 15.3 0 01-4 10 15.3 15.3 0 01-4-10 15.3 15.3 0 014-10z"/>
</svg>

<!-- Shield / Certificats -->
<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"
     stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
    <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>
</svg>

<!-- Document / Logs -->
<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"
     stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
    <path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z"/>
    <polyline points="14 2 14 8 20 8"/>
    <line x1="16" y1="13" x2="8" y2="13"/>
    <line x1="16" y1="17" x2="8" y2="17"/>
</svg>

<!-- Mail / Emails -->
<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"
     stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
    <path d="M4 4h16c1.1 0 2 .9 2 2v12c0 1.1-.9 2-2 2H4c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2z"/>
    <polyline points="22,6 12,13 2,6"/>
</svg>

<!-- Lock / Secrets -->
<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"
     stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
    <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/>
    <path d="M7 11V7a5 5 0 0110 0v4"/>
</svg>

<!-- Plus (creer) -->
<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4"/>
</svg>

<!-- Trash (supprimer) -->
<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
          d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"/>
</svg>

<!-- Eye (voir detail) -->
<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
          d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"/>
    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
          d="M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z"/>
</svg>

<!-- X (fermer) -->
<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"/>
</svg>

<!-- Chevron right (breadcrumb, sous-menu) -->
<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7"/>
</svg>

<!-- Chevron down (dropdown) -->
<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"/>
</svg>

<!-- Refresh/Sync -->
<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
          d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"/>
</svg>

<!-- Layers / Logo SocleHub -->
<svg class="w-7 h-7 text-indigo-600" viewBox="0 0 24 24" fill="none" stroke="currentColor"
     stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
    <path d="M12 2L2 7l10 5 10-5-10-5z"/>
    <path d="M2 17l10 5 10-5"/>
    <path d="M2 12l10 5 10-5"/>
</svg>
```

---

## Annexe B : Dimensions et espacements de reference

| Element | Dimension | Classes |
|---------|-----------|---------|
| Topbar hauteur | 56px | `h-14` |
| Sidebar largeur | 256px | `w-64` |
| Main padding | 24px | `p-6` |
| Gap entre cartes | 24px | `gap-6` |
| Gap dans grille stats | 16px | `gap-4` |
| Padding carte | 24px | `p-6` |
| Padding carte stats | 16px | `p-4` |
| Border radius carte | 12px | `rounded-xl` |
| Border radius bouton | 8px | `rounded-lg` |
| Border radius badge | 9999px | `rounded-full` |
| Border radius input | 8px | `rounded-lg` |
| Taille texte H1 | 24px | `text-2xl` |
| Taille texte H2 | 16px | `text-base` |
| Taille texte body | 14px | `text-sm` |
| Taille texte badge | 12px | `text-xs` |
| Taille texte code | 12px | `text-xs font-mono` |
| Icone sidebar | 20px | `w-5 h-5` |
| Icone action | 16px | `w-4 h-4` |
| Icone empty state | 48px | `w-12 h-12` |
| Avatar | 32px | `w-8 h-8` |
| Status dot | 6px | `w-1.5 h-1.5` |
